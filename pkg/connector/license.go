package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

var licenses = []string{creator, explorer, viewer, unlicensed}

var licensesMap = map[string]string{
	"creator":    creator,
	"explorer":   explorer,
	"viewer":     viewer,
	"unlicensed": unlicensed,
}

// RolesPerLicense groups the Tableau site roles that consume each license tier.
// Doc: https://help.tableau.com/current/server/en-us/permission_license_siterole.htm
var RolesPerLicense = map[string][]string{
	creator:    {creator, siteAdministratorCreator, serverAdministrator},
	explorer:   {explorer, siteAdministratorExplorer, explorerCanPublish, siteAdministrator},
	viewer:     {viewer},
	unlicensed: {unlicensed},
}

// licenseCapacity returns the raw per-tier capacity string, or nil when the site exposes none.
func licenseCapacity(license string, site *client.Site) *string {
	if site == nil {
		return nil
	}
	switch license {
	case creator:
		return site.TierCreatorCapacity
	case explorer:
		return site.TierExplorerCapacity
	case viewer:
		return site.TierViewerCapacity
	default:
		return nil
	}
}

// capacitySeats parses a tier-capacity string, reporting seats only for a positive cap.
func capacitySeats(capacity *string) (int64, bool) {
	if capacity == nil {
		return 0, false
	}
	netSeats, err := strconv.ParseInt(strings.TrimSpace(*capacity), 10, 64)
	if err != nil || netSeats <= 0 {
		return 0, false
	}
	return netSeats, true
}

// hasTierCapacity reports whether the site exposes any positive per-tier capacity.
func hasTierCapacity(site *client.Site) bool {
	if site == nil {
		return false
	}
	for _, capacity := range []*string{site.TierCreatorCapacity, site.TierExplorerCapacity, site.TierViewerCapacity} {
		if _, ok := capacitySeats(capacity); ok {
			return true
		}
	}
	return false
}

type licenseBuilder struct {
	client *client.Client
}

func (l *licenseBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeLicense
}

// licenseResource builds a License resource, attaching seat counts only when the tier is capped.
func licenseResource(license string, purchased *string, consumed int64) (*v2.Resource, error) {
	licenseID := strings.ToLower(license)
	profile := map[string]any{
		"license_name": license,
		"license_id":   licenseID,
	}

	roleTraitOptions := []rs.RoleTraitOption{
		rs.WithRoleProfile(profile),
	}

	stub := &v2.Resource{
		Id: &v2.ResourceId{
			ResourceType: resourceTypeLicense.Id,
			Resource:     licenseID,
		},
	}
	licenseTraitOptions := []rs.LicenseProfileTraitOption{
		rs.WithLicenseName(license),
		rs.WithLicenseEntitlementIDs(ent.NewEntitlementID(stub, memberEntitlement)),
	}
	if seats, ok := capacitySeats(purchased); ok {
		licenseTraitOptions = append(licenseTraitOptions, rs.WithLicenseSeats(seats, consumed))
	}

	ret, err := rs.NewRoleResource(license, resourceTypeLicense, licenseID, roleTraitOptions,
		rs.WithLicenseProfileTrait(licenseTraitOptions...),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// List emits the four license tiers, enriched with purchased/consumed seats on a best-effort basis.
func (l *licenseBuilder) List(ctx context.Context, _ *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	log := ctxzap.Extract(ctx)

	site, _, err := l.client.GetSite(ctx)
	if err != nil {
		log.Debug("failed to get site for license capacities; syncing licenses without seat counts", zap.Error(err))
		site = nil
	}

	consumed := map[string]int64{}
	countOK := false
	if hasTierCapacity(site) {
		consumedByTier, cErr := l.countConsumedByTier(ctx)
		if cErr != nil {
			log.Debug("failed to count consumed license seats; syncing licenses without seat counts", zap.Error(cErr))
		} else {
			consumed = consumedByTier
			countOK = true
		}
	}

	var rv []*v2.Resource
	for _, license := range licenses {
		var purchased *string
		if countOK {
			purchased = licenseCapacity(license, site)
		}
		res, err := licenseResource(license, purchased, consumed[license])
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create license resource %s: %w", license, err)
		}
		rv = append(rv, res)
	}

	return rv, nil, nil
}

// countConsumedByTier counts users per license tier server-side via the site-role
// filter, reading the pagination total instead of paging through every user.
func (l *licenseBuilder) countConsumedByTier(ctx context.Context) (map[string]int64, error) {
	consumed := make(map[string]int64, len(licenses))
	for _, license := range licenses {
		filterable := filterableRoles(RolesPerLicense[license])
		if len(filterable) == 0 {
			continue
		}
		count, _, err := l.client.CountUsers(ctx, siteRoleFilter(filterable))
		if err != nil {
			return nil, fmt.Errorf("failed to count %s users: %w", license, err)
		}
		consumed[license] = count
	}
	return consumed, nil
}

func (l *licenseBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// StaticEntitlements declares the single "member" entitlement every license tier exposes.
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

// Grants emits one member grant per user holding a site role that consumes this tier.
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

// Grant assigns the target license tier by setting the user's site role.
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

// Revoke removes the license by setting the user's site role to Unlicensed.
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
