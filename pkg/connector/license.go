package connector

import (
	"context"
	"fmt"
	"slices"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
)

var licenses = []string{creator, explorer, viewer, unlicensed}
var licensesMap = map[string]string{
	"creator":    creator,
	"explorer":   explorer,
	"viewer":     viewer,
	"unlicensed": unlicensed,
}
var RolesPerLicense = map[string][]string{
	creator:    {creator, siteAdministratorCreator},
	explorer:   {explorer, siteAdministratorExplorer, explorerCanPublish, readOnly, siteAdministrator},
	viewer:     {viewer},
	unlicensed: {unlicensed},
}

type licenseBuilder struct {
	client *client.Client
}

func (l *licenseBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeLicense
}

// Create a new connector resource for a Tableau License.
func licenseResource(license string) (*v2.Resource, error) {
	licenseID := strings.ToLower(license)
	profile := map[string]any{
		"license_name": license,
		"license_id":   licenseID,
	}

	roleTraitOptions := []rs.RoleTraitOption{
		rs.WithRoleProfile(profile),
	}

	ret, err := rs.NewRoleResource(license, resourceTypeLicense, licenseID, roleTraitOptions)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (l *licenseBuilder) List(_ context.Context, _ *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource

	for _, license := range licenses {
		sr, err := licenseResource(license)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, sr)
	}

	return rv, "", nil, nil
}

func (l *licenseBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (l *licenseBuilder) StaticEntitlements(_ context.Context, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	en := ent.NewAssignmentEntitlement(
		nil,
		memberEntitlement,
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDisplayName("License member"),
		ent.WithDescription("Member of License in Tableau"),
	)

	return []*v2.Entitlement{en}, "", nil, nil
}

func (l *licenseBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, nextToken, _, err := l.client.GetUsers(ctx, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list users: %w", err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		userResource, err := userResource(&user, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		if slices.Contains(RolesPerLicense[resource.DisplayName], user.SiteRole) {
			gr := grant.NewGrant(resource, memberEntitlement, userResource.Id)
			rv = append(rv, gr)
		}
	}

	return rv, nextToken, nil, nil
}

func (l *licenseBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	licenseName, ok := licensesMap[entitlement.Resource.Id.Resource]
	if !ok {
		return nil, fmt.Errorf("unknown license %s", entitlement.Resource.Id.Resource)
	}

	annos, err := l.client.UpdateUserSiteRole(ctx, principal.Id.Resource, licenseName)
	if err != nil {
		return annos, fmt.Errorf("failed to grant %s license to user %s: %w", licenseName, principal.Id.Resource, err)
	}

	return annos, nil
}

func (l *licenseBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	principalID := grant.Principal.Id.Resource

	annos, err := l.client.UpdateUserSiteRole(ctx, principalID, unlicensed)
	if err != nil {
		return annos, fmt.Errorf("failed to revoke license from user %s: %w", principalID, err)
	}

	return annos, nil
}

func newLicenseBuilder(client *client.Client) *licenseBuilder {
	return &licenseBuilder{
		client: client,
	}
}
