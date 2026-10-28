package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Valid Tableau REST API site roles.
// See: https://help.tableau.com/current/api/rest_api/en-us/REST/rest_api_concepts_new_site_roles.htm
const (
	siteAdministrator         = "SiteAdministrator"
	siteAdministratorCreator  = "SiteAdministratorCreator"
	siteAdministratorExplorer = "SiteAdministratorExplorer"
	serverAdministrator       = "ServerAdministrator"
	creator                   = "Creator"
	explorer                  = "Explorer"
	explorerCanPublish        = "ExplorerCanPublish"
	viewer                    = "Viewer"
	unlicensed                = "Unlicensed"
)

// roles maps Tableau API role names to display slugs.
// ServerAdministrator is included for sync (Tableau returns it for server-level
// admins) but this connector does not assign it — it exists only on Tableau
// Server.
var roles = map[string]string{
	siteAdministrator:         "site administrator",
	siteAdministratorCreator:  "site administrator creator",
	siteAdministratorExplorer: "site administrator explorer",
	serverAdministrator:       "server administrator",
	creator:                   "creator",
	explorer:                  "explorer",
	explorerCanPublish:        "explorer can publish",
	viewer:                    "viewer",
	unlicensed:                "unlicensed",
}

// nonGrantableRoles are roles that can appear during sync but that this
// connector does not assign.
var nonGrantableRoles = map[string]bool{
	serverAdministrator: true,
}

type siteBuilder struct {
	client *client.Client
}

func (s *siteBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeSite
}

// Create a new connector resource for a Tableau site.
func siteResource(site *client.Site) (*v2.Resource, error) {
	siteOptions := []rs.ResourceOption{
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeUser.Id},
			&v2.ChildResourceType{ResourceTypeId: resourceTypeGroup.Id},
			&v2.ChildResourceType{ResourceTypeId: resourceTypeProject.Id},
		),
	}
	ret, err := rs.NewResource(site.Name, resourceTypeSite, site.ID, siteOptions...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *siteBuilder) List(ctx context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var rv []*v2.Resource
	site, _, err := s.client.GetSite(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get site: %w", err)
	}
	siteResource, err := siteResource(site)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build site resource: %w", err)
	}
	rv = append(rv, siteResource)

	return rv, nil, nil
}

func (s *siteBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	var rv []*v2.Entitlement
	for _, role := range roles {
		permissionOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser),
			ent.WithDescription(fmt.Sprintf("Role in %s Tableau site", resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s Site %s", resource.DisplayName, role)),
		}

		permissionEntitlement := ent.NewPermissionEntitlement(resource, role, permissionOptions...)
		rv = append(rv, permissionEntitlement)
	}
	return rv, nil, nil
}

func (s *siteBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)
	users, nextToken, _, err := s.client.GetUsers(ctx, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list users: %w", err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		roleName := roles[user.SiteRole]
		if roleName == "" {
			l.Debug("skipping user with unknown site role",
				zap.String("site_role", user.SiteRole),
				zap.String("user_id", user.ID),
				zap.String("user_name", user.FullName),
			)
			continue
		}
		userResource, err := userResource(user, resource.Id)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build user resource for %s: %w", user.ID, err)
		}

		permissionGrant := grant.NewGrant(resource, roleName, userResource.Id)
		rv = append(rv, permissionGrant)
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

// apiRoleForSlug maps a Site Role entitlement slug back to the Tableau API role name.
func apiRoleForSlug(slug string) (string, bool) {
	for key, value := range roles {
		if value == slug {
			return key, true
		}
	}
	return "", false
}

// Grant sets the user's site role to the entitlement's role.
//
// A Tableau user has exactly one site role, so Grant reads the current role
// (bypassing the response cache) before writing:
//   - already holding the target role -> no write, GrantAlreadyExists.
//   - otherwise -> write the role and, when the previous role is a known site
//     role, report GrantReplaced naming the site-role grant the write displaced.
//
// Rate-limit annotations from the initial read are merged into every result.
func (s *siteBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)

	roleName, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse role from entitlement ID: %w", err)
	}
	principalID := principal.Id.Resource

	apiRoleName, ok := apiRoleForSlug(roleName)
	if !ok {
		return nil, fmt.Errorf("unknown role: %s", roleName)
	}

	if nonGrantableRoles[apiRoleName] {
		return nil, fmt.Errorf("role %q is not assigned by this connector (it exists only on Tableau Server)", apiRoleName)
	}

	user, getAnnos, err := s.client.GetUser(ctx, principalID)
	if err != nil {
		if client.IsNotFoundError(err) {
			return getAnnos, status.Errorf(codes.NotFound, "site role: cannot grant %s: user %s not found", apiRoleName, principalID)
		}
		return getAnnos, fmt.Errorf("failed to read user %s for site role grant: %w", principalID, err)
	}

	if user.SiteRole == apiRoleName {
		log.Debug("site role grant: user already holds role, no write needed",
			zap.String("user_id", principalID), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyExists{})), nil
	}

	putAnnos, err := s.client.UpdateUserSiteRole(ctx, principalID, apiRoleName)
	annos := mergeAnnotations(getAnnos, putAnnos)
	if err != nil {
		return annos, fmt.Errorf("failed to grant %s role to user %s: %w", roleName, principalID, err)
	}

	if oldSlug, known := roles[user.SiteRole]; known {
		oldEntitlement := ent.NewPermissionEntitlement(entitlement.Resource, oldSlug)
		annos.Append(&v2.GrantReplaced{ReplacedGrantId: grant.NewGrantID(principal.Id, oldEntitlement)})
	}

	return annos, nil
}

// Revoke resets the user to Unlicensed, but only when they still hold the
// revoked role.
//
// Revoke reads the user's current role first: a 404, or a role other than the
// one being revoked (e.g. a later Grant replaced it, see Grant's GrantReplaced
// annotation), means there is nothing left to revoke, and writing Unlicensed
// would wipe out access this revoke was never asked to touch. Revoking
// Unlicensed from a user who is Unlicensed is the same no-op: it is the floor
// role and has nothing to remove. Rate-limit annotations from the initial read
// are merged into every result.
func (s *siteBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)
	principalID := g.Principal.Id.Resource

	roleName, err := parseEntitlementSlug(g.Entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse role from entitlement ID: %w", err)
	}
	apiRoleName, ok := apiRoleForSlug(roleName)
	if !ok {
		return nil, fmt.Errorf("unknown role: %s", roleName)
	}

	user, getAnnos, err := s.client.GetUser(ctx, principalID)
	if err != nil {
		if client.IsNotFoundError(err) {
			log.Debug("site role revoke: user not found, nothing to revoke", zap.String("user_id", principalID))
			return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
		}
		return getAnnos, fmt.Errorf("failed to read user %s for site role revoke: %w", principalID, err)
	}

	if user.SiteRole != apiRoleName {
		log.Debug("site role revoke: user no longer holds role, nothing to revoke",
			zap.String("user_id", principalID), zap.String("role", apiRoleName), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
	}

	if apiRoleName == unlicensed {
		log.Debug("site role revoke: user already Unlicensed (floor role), nothing to remove",
			zap.String("user_id", principalID))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
	}

	putAnnos, err := s.client.UpdateUserSiteRole(ctx, principalID, unlicensed)
	annos := mergeAnnotations(getAnnos, putAnnos)
	if err != nil {
		return annos, fmt.Errorf("failed to revoke site role from user %s: %w", principalID, err)
	}

	return annos, nil
}

func newSiteBuilder(client *client.Client) *siteBuilder {
	return &siteBuilder{
		client: client,
	}
}
