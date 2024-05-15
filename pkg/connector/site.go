package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-tableau/pkg/tableau"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"

	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
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

type siteResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (o *siteResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// Create a new connector resource for a Tableau site.
func siteResource(site tableau.Site) (*v2.Resource, error) {
	siteOptions := []rs.ResourceOption{
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeUser.Id},
			&v2.ChildResourceType{ResourceTypeId: resourceTypeGroup.Id},
		),
	}
	ret, err := rs.NewResource(site.Name, resourceTypeSite, site.ID, siteOptions...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *siteResourceType) List(ctx context.Context, _ *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource
	site, err := o.client.GetSite(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	sr, err := siteResource(site)
	if err != nil {
		return nil, "", nil, err
	}
	rv = append(rv, sr)

	return rv, "", nil, nil
}

func (o *siteResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement
	for _, role := range roles {
		permissionOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser),
			ent.WithDescription(fmt.Sprintf("Role in %s Tableau site", resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s Site %s", resource.DisplayName, role)),
		}

		permissionEn := ent.NewPermissionEntitlement(resource, role, permissionOptions...)
		rv = append(rv, permissionEn)
	}
	return rv, "", nil, nil
}

func (o *siteResourceType) Grants(ctx context.Context, resource *v2.Resource, pt *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, err := o.client.GetPaginatedUsers(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	var rv []*v2.Grant
	for _, user := range users {
		roleName := roles[user.SiteRole]
		if roleName == "" {
			ctxzap.Extract(ctx).Warn("Unknown Tableau Role Name",
				zap.String("role_name", user.SiteRole),
				zap.String("user", user.FullName),
			)
		}
		userCopy := user
		ur, err := userResource(ctx, &userCopy, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		permissionGrant := grant.NewGrant(resource, roleName, ur.Id)
		rv = append(rv, permissionGrant)
	}
	return rv, "", nil, nil
}

func siteBuilder(client *tableau.Client) *siteResourceType {
	return &siteResourceType{
		resourceType: resourceTypeSite,
		client:       client,
	}
}
