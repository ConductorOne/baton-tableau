package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"

	"github.com/conductorone/baton-tableau/pkg/tableau"

	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
)

var licences = []string{creator, explorer, viewer, unlicensed}
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

type licenseResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (l *licenseResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return l.resourceType
}

// Create a new connector resource for a Tableau License.
func licenseResource(license string) (*v2.Resource, error) {
	licenseID := strings.ToLower(license)
	profile := map[string]interface{}{
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

func (l *licenseResourceType) List(ctx context.Context, _ *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource

	for _, license := range licences {
		sr, err := licenseResource(license)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, sr)
	}

	return rv, "", nil, nil
}

func (l *licenseResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDescription(fmt.Sprintf("Member of %s License in Tableau", resource.DisplayName)),
		ent.WithDisplayName(fmt.Sprintf("%s License %s", resource.DisplayName, memberEntitlement)),
	}

	en := ent.NewAssignmentEntitlement(resource, memberEntitlement, assigmentOptions...)
	rv = append(rv, en)

	return rv, "", nil, nil
}

func (l *licenseResourceType) Grants(ctx context.Context, resource *v2.Resource, pt *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	users, token, err := l.client.GetUsersPage(ctx, pt.Token)
	if err != nil {
		return nil, "", nil, err
	}

	var rv []*v2.Grant
	for _, user := range users {
		userCopy := user
		ur, err := userResource(&userCopy, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		if licenseContainsRole(resource.DisplayName, user.SiteRole, RolesPerLicense) {
			gr := grant.NewGrant(resource, memberEntitlement, ur.Id)
			rv = append(rv, gr)
		}
	}

	return rv, token, nil, nil
}

func (l *licenseResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	licenseName, ok := licensesMap[entitlement.Resource.Id.Resource]
	if !ok {
		return nil, fmt.Errorf("unknown license %s", entitlement.Resource.Id.Resource)
	}
	principalID := principal.Id.Resource

	outputAnnotations := annotations.New()
	err := l.client.UpdateUserSiteRole(ctx, principalID, licenseName)
	if err != nil {
		return outputAnnotations, fmt.Errorf("failed to grant %s license to user %s: %w", licenseName, principalID, err)
	}

	return outputAnnotations, nil
}

func (l *licenseResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	principalID := grant.Principal.Id.Resource

	outputAnnotations := annotations.New()
	err := l.client.UpdateUserSiteRole(ctx, principalID, unlicensed)
	if err != nil {
		return outputAnnotations, fmt.Errorf("failed to revoke license from user %s: %w", principalID, err)
	}

	return outputAnnotations, nil
}

func licenseBuilder(client *tableau.Client) *licenseResourceType {
	return &licenseResourceType{
		resourceType: resourceTypeLicense,
		client:       client,
	}
}

func licenseContainsRole(license string, role string, licencesMap map[string][]string) bool {
	slice, ok := licencesMap[license]
	if !ok {
		return false
	}

	for _, value := range slice {
		if value == role {
			return true
		}
	}

	return false
}
