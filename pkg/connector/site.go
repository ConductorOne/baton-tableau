package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

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
	readOnly                  = "ReadOnly"
)

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
	readOnly:                  "readonly",
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

func (s *siteBuilder) List(ctx context.Context, _ *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource
	site, _, err := s.client.GetSite(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get site: %w", err)
	}
	siteResource, err := siteResource(site)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to build site resource: %w", err)
	}
	rv = append(rv, siteResource)

	return rv, "", nil, nil
}

func (s *siteBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
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
	return rv, "", nil, nil
}

func (s *siteBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, nextToken, _, err := s.client.GetUsers(ctx, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list users: %w", err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		roleName := roles[user.SiteRole]
		if roleName == "" {
			ctxzap.Extract(ctx).Debug("Unknown Tableau Role Name",
				zap.String("role_name", user.SiteRole),
				zap.String("user", user.FullName),
			)
		}
		userResource, err := userResource(&user, resource.Id)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to build user resource for %s: %w", user.ID, err)
		}

		permissionGrant := grant.NewGrant(resource, roleName, userResource.Id)
		rv = append(rv, permissionGrant)
	}

	return rv, nextToken, nil, nil
}

func (s *siteBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	roleName, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse role from entitlement ID: %w", err)
	}
	principalID := principal.Id.Resource

	var apiRoleName string
	for key, value := range roles {
		if value == roleName {
			apiRoleName = key
			break
		}
	}

	if apiRoleName == "" {
		return nil, fmt.Errorf("unknown role: %s", roleName)
	}

	annos, err := s.client.UpdateUserSiteRole(ctx, principalID, apiRoleName)
	if err != nil {
		return annos, fmt.Errorf("failed to grant %s role to user %s: %w", roleName, principalID, err)
	}

	return annos, nil
}

func (s *siteBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	principalID := grant.Principal.Id.Resource

	annos, err := s.client.UpdateUserSiteRole(ctx, principalID, unlicensed)
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
