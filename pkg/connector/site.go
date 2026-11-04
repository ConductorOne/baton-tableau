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
			ent.WithExclusionGroup(siteRoleExclusionGroup(resource)),
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

// siteRoleExclusionGroup is shared by one site's Site Role entitlements: a user
// holds one role per site, and the site ID keeps different sites apart.
func siteRoleExclusionGroup(site *v2.Resource) string {
	return "site-role:" + site.Id.Resource
}

// errServerAdminProtected refuses a grant over a ServerAdministrator: a
// server-wide role this connector cannot restore, so it could not undo the write.
func errServerAdminProtected(action, userID string) error {
	return status.Errorf(codes.FailedPrecondition,
		"site role: cannot %s for user %s: they hold %s, a server-wide role this connector cannot restore; a server administrator has to change it in Tableau",
		action, userID, serverAdministrator)
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

// Grant sets the user's site role to the entitlement's role. A user who already
// holds it gets GrantAlreadyExists; a ServerAdministrator is refused with
// FailedPrecondition. Any other role is replaced, reporting GrantReplaced.
func (s *siteBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
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

	rc := s.roleChange(principalID, apiRoleName)
	rc.refuseGrant = func(siteRole string) error {
		if siteRole != serverAdministrator {
			return nil
		}
		return errServerAdminProtected("grant "+apiRoleName, principalID)
	}
	rc.replacedGrantID = func(siteRole string) (string, bool) {
		oldSlug, known := roles[siteRole]
		if !known {
			return "", false
		}
		return grant.NewGrantID(principal.Id, ent.NewPermissionEntitlement(entitlement.Resource, oldSlug)), true
	}

	return rc.grantRole(ctx, apiRoleName)
}

// Revoke resets the user to Unlicensed while they still hold the revoked role,
// including any administrator role. A missing user, a different role, or an
// already Unlicensed user is GrantAlreadyRevoked with no write.
func (s *siteBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	roleName, err := parseEntitlementSlug(g.Entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse role from entitlement ID: %w", err)
	}
	apiRoleName, ok := apiRoleForSlug(roleName)
	if !ok {
		return nil, fmt.Errorf("unknown role: %s", roleName)
	}

	return s.roleChange(g.Principal.Id.Resource, apiRoleName).revokeRole(ctx)
}

// roleChange builds the shared role write for one user and site role.
func (s *siteBuilder) roleChange(userID, apiRoleName string) *roleChange {
	return &roleChange{
		client:  s.client,
		label:   "site role",
		subject: apiRoleName,
		userID:  userID,
		holds:   func(siteRole string) bool { return siteRole == apiRoleName },
	}
}

func newSiteBuilder(client *client.Client) *siteBuilder {
	return &siteBuilder{
		client: client,
	}
}
