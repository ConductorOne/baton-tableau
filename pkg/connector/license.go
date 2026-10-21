package connector

import (
	"context"
	"fmt"
	"slices"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// adminLicenseRoles are the site roles a cross-tier license Grant must never
// silently demote. A license tier is a derived view of a user's single site
// role, so granting a different tier is itself a role change; stripping an
// admin role as a side effect of that change would be an unannounced
// privilege reduction. Use the Site Role entitlement to change an admin role
// explicitly instead.
var adminLicenseRoles = map[string]bool{
	siteAdministrator:         true,
	siteAdministratorCreator:  true,
	siteAdministratorExplorer: true,
	serverAdministrator:       true,
}

// tierForRole returns the license tier whose RolesPerLicense set contains role.
func tierForRole(role string) (string, bool) {
	for _, tier := range licenses {
		if slices.Contains(RolesPerLicense[tier], role) {
			return tier, true
		}
	}
	return "", false
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

	roleTraitOptions := []rs.RoleTraitOption{}

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
		rs.WithResourceProfile(profile),
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
		consumedByTier, cErr := l.countConsumedByTier(ctx, site)
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

// countConsumedByTier counts users server-side per capped tier via the site-role
// filter, reading the pagination total instead of paging through every user.
func (l *licenseBuilder) countConsumedByTier(ctx context.Context, site *client.Site) (map[string]int64, error) {
	consumed := make(map[string]int64, len(licenses))
	for _, license := range licenses {
		if _, ok := capacitySeats(licenseCapacity(license, site)); !ok {
			continue
		}
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
//
// A Tableau user has exactly one site role, and RolesPerLicense groups which
// roles satisfy which tier, so a license tier is a derived view of that one
// role rather than an independent attribute. Grant therefore reads the user's
// current role before writing:
//   - already in the target tier -> no write, GrantAlreadyExists.
//   - current role is an admin role outside the tier -> refuse with
//     FailedPrecondition; a license grant must not double as a silent
//     demotion, and ServerAdministrator cannot be restored via this API.
//   - otherwise -> write the tier's base role and, when the user's previous
//     role belonged to another tier, report GrantReplaced naming the license
//     grant the write displaced (tiers are exclusive, so a cross-tier grant
//     is itself the requested role change).
//
// Rate-limit annotations from the initial read are merged into every result.
func (l *licenseBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)

	targetTier, ok := licensesMap[entitlement.Resource.Id.Resource]
	if !ok {
		return nil, fmt.Errorf("unknown license %s", entitlement.Resource.Id.Resource)
	}

	user, getAnnos, err := l.client.GetUser(ctx, principal.Id.Resource)
	if err != nil {
		if client.IsNotFoundError(err) {
			return getAnnos, status.Errorf(codes.NotFound, "license: cannot grant %s: user %s not found", targetTier, principal.Id.Resource)
		}
		return getAnnos, fmt.Errorf("failed to read user %s for license grant: %w", principal.Id.Resource, err)
	}

	if slices.Contains(RolesPerLicense[targetTier], user.SiteRole) {
		log.Debug("license grant: user already in target tier, no write needed",
			zap.String("user_id", principal.Id.Resource), zap.String("tier", targetTier), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyExists{})), nil
	}

	if adminLicenseRoles[user.SiteRole] {
		log.Warn("license grant: refusing to move an admin role across license tiers",
			zap.String("user_id", principal.Id.Resource), zap.String("tier", targetTier), zap.String("site_role", user.SiteRole))
		return getAnnos, status.Errorf(codes.FailedPrecondition,
			"license: cannot grant %s to user %s: current site role %q is an administrative role; use the Site Role entitlement to change it explicitly",
			targetTier, principal.Id.Resource, user.SiteRole)
	}

	putAnnos, err := l.client.UpdateUserSiteRole(ctx, principal.Id.Resource, targetTier)
	annos := mergeAnnotations(getAnnos, putAnnos)
	if err != nil {
		return annos, fmt.Errorf("failed to grant %s license to user %s: %w", targetTier, principal.Id.Resource, err)
	}

	if oldTier, ok := tierForRole(user.SiteRole); ok && oldTier != targetTier {
		oldResource := &v2.Resource{
			Id: &v2.ResourceId{
				ResourceType: resourceTypeLicense.Id,
				Resource:     strings.ToLower(oldTier),
			},
		}
		oldEntitlement := ent.NewAssignmentEntitlement(oldResource, memberEntitlement)
		annos.Append(&v2.GrantReplaced{ReplacedGrantId: grant.NewGrantID(principal.Id, oldEntitlement)})
	}

	return annos, nil
}

// Revoke removes the license by setting the user's site role to Unlicensed.
//
// Revoke reads the user's current role first: a 404, or a role that has
// already moved out of this tier (e.g. a later Grant replaced it, see Grant's
// GrantReplaced annotation), means there is nothing left to revoke. Writing
// Unlicensed in that case would wipe out access this revoke was never asked
// to touch. Revoking the Unlicensed tier from a user who is Unlicensed is
// rejected with FailedPrecondition: it is the floor tier and has nothing to
// remove. Rate-limit annotations from the initial read are merged into every
// result.
func (l *licenseBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)
	principalID := g.Principal.Id.Resource

	tier, ok := licensesMap[g.Entitlement.Resource.Id.Resource]
	if !ok {
		return nil, fmt.Errorf("unknown license %s", g.Entitlement.Resource.Id.Resource)
	}

	user, getAnnos, err := l.client.GetUser(ctx, principalID)
	if err != nil {
		if client.IsNotFoundError(err) {
			log.Debug("license revoke: user not found, nothing to revoke", zap.String("user_id", principalID))
			return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
		}
		return getAnnos, fmt.Errorf("failed to read user %s for license revoke: %w", principalID, err)
	}

	if !slices.Contains(RolesPerLicense[tier], user.SiteRole) {
		log.Debug("license revoke: user no longer in tier, nothing to revoke",
			zap.String("user_id", principalID), zap.String("tier", tier), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
	}

	if tier == unlicensed {
		return getAnnos, status.Errorf(codes.FailedPrecondition,
			"license: cannot revoke the Unlicensed tier from user %s: Unlicensed is the floor tier and has nothing to remove", principalID)
	}

	putAnnos, err := l.client.UpdateUserSiteRole(ctx, principalID, unlicensed)
	annos := mergeAnnotations(getAnnos, putAnnos)
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
