package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
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
	explorer:   {explorer, siteAdministratorExplorer, explorerCanPublish, siteAdministrator},
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

func (l *licenseBuilder) List(_ context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var rv []*v2.Resource

	for _, license := range licenses {
		sr, err := licenseResource(license)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create license resource %s: %w", license, err)
		}
		rv = append(rv, sr)
	}

	return rv, nil, nil
}

func (l *licenseBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (l *licenseBuilder) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	en := ent.NewAssignmentEntitlement(
		nil,
		memberEntitlement,
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDisplayName("License member"),
		ent.WithDescription("Member of License in Tableau"),
	)

	return []*v2.Entitlement{en}, nil, nil
}

func (l *licenseBuilder) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	allRoles := RolesPerLicense[resource.DisplayName]
	if len(allRoles) == 0 {
		return nil, nil, nil
	}

	roleSet := make(map[string]bool, len(allRoles))
	for _, r := range allRoles {
		roleSet[r] = true
	}

	filterable := filterableRoles(allRoles)

	var reqOpts []client.ReqOpt
	if len(filterable) == len(allRoles) && len(filterable) > 0 {
		reqOpts = append(reqOpts, siteRoleFilter(filterable))
	}

	users, nextToken, _, err := l.client.GetUsers(ctx, opts.PageToken.Token, reqOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list users for license %s: %w", resource.DisplayName, err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		if !roleSet[user.SiteRole] {
			continue
		}

		userResource, err := userResource(user, resource.Id)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build user resource for %s: %w", user.ID, err)
		}

		gr := grant.NewGrant(resource, memberEntitlement, userResource.Id)
		rv = append(rv, gr)
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextToken}, nil
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
