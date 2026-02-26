package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	memberEntitlement = "member"
	allowMode         = "Allow"

	Read               = "Read"
	Write              = "Write"
	Delete             = "Delete"
	Filter             = "Filter"
	ViewComments       = "ViewComments"
	AddComment         = "AddComment"
	ExportImage        = "ExportImage"
	ExportData         = "ExportData"
	ShareView          = "ShareView"
	ViewUnderlyingData = "ViewUnderlyingData"
	WebAuthoring       = "WebAuthoring"
	RunExplainData     = "RunExplainData"
	ExportXml          = "ExportXml"
	ExtractRefresh     = "ExtractRefresh"
	ChangeHierarchy    = "ChangeHierarchy"
	ChangePermissions  = "ChangePermissions"
)

// allCapabilities maps every known Tableau capability key to its display name.
var allCapabilitiesMap = map[string]string{
	Read:               "View",
	Write:              "Overwrite",
	Delete:             "Delete",
	Filter:             "Filter",
	ViewComments:       "View Comments",
	AddComment:         "Add Comments",
	ExportImage:        "Download Image/PDF",
	ExportData:         "Download Summary Data",
	ShareView:          "Share Customized",
	ViewUnderlyingData: "Download Full Data",
	WebAuthoring:       "Web Edit",
	RunExplainData:     "Run Explain Data",
	ExportXml:          "Download/Save A Copy",
	ExtractRefresh:     "Extract Refresh",
	ChangeHierarchy:    "Move",
	ChangePermissions:  "Set Permissions",
}

var projectCapabilitiesMap = map[string]string{
	Write: "Publish",
}

// pickCapabilities returns a capability map containing only the requested keys.
func pickCapabilities(keys ...string) map[string]string {
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = allCapabilitiesMap[k]
	}
	return m
}

// pickProjectCapabilities returns the capabilities that can be applied at the project level,
// applying project-specific display name overrides where defined.
func pickProjectCapabilities(keys ...string) map[string]string {
	capabilities := pickCapabilities(keys...)
	for _, k := range keys {
		if override, ok := projectCapabilitiesMap[k]; ok {
			capabilities[k] = override
		}
	}
	return capabilities
}

// capabilityByDisplaySlug is the reverse of allCapabilitiesMap and projectCapabilitiesMap:
// maps display name slugs back to Tableau API capability names.
// e.g., "View" → "Read", "Publish" → "Write", "Add Comments" → "AddComment"
var capabilityByDisplaySlug = func() map[string]string {
	m := make(map[string]string, len(allCapabilitiesMap)+len(projectCapabilitiesMap))
	for capName, slug := range allCapabilitiesMap {
		m[slug] = capName
	}
	// Project overrides take precedence (e.g. "Publish" → "Write")
	for capName, slug := range projectCapabilitiesMap {
		m[slug] = capName
	}
	return m
}()

// parseEntitlementSlug extracts the slug (last segment) from an entitlement ID.
// Entitlement IDs have the format "resourceType:resourceId:slug".
func parseEntitlementSlug(entitlementID string) (string, error) {
	parts := strings.Split(entitlementID, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid entitlement ID: %s", entitlementID)
	}
	return parts[2], nil
}

// staticPermissionEntitlements creates static permission entitlements (nil resource)
// that the SDK applies to every resource of the given type.
func staticPermissionEntitlements(capabilities map[string]string, resourceLabel string, grantableTo ...*v2.ResourceType) []*v2.Entitlement {
	grantableTo = append(grantableTo, resourceTypeUser, resourceTypeGroup)
	
	var rv []*v2.Entitlement
	for key, displayName := range capabilities {
		entitlement := ent.NewPermissionEntitlement(nil, key,
			ent.WithGrantableTo(grantableTo...),
			ent.WithSlug(displayName),
			ent.WithDisplayName(displayName),
			ent.WithDescription(fmt.Sprintf("%s permission for %s", displayName, resourceLabel)),
		)
		rv = append(rv, entitlement)
	}
	return rv
}

// slugFromMap returns the display slug for capName using m as a lookup table.
// Falls back to allCapabilitiesMap, then capName itself if nothing matches.
func slugFromMap(m map[string]string, capName string) string {
	if m != nil {
		if slug, ok := m[capName]; ok {
			return slug
		}
	}
	if slug, ok := allCapabilitiesMap[capName]; ok {
		return slug
	}
	return capName
}

// grantsFromCapabilities creates grants from Tableau grantee capabilities.
//
// ownSlugMap maps Tableau capability names to the display slugs used for the
// grant's own entitlement reference. If nil, allCapabilitiesMap is used as fallback.
//
// For group grantees, a GrantExpandable annotation is added so the SDK expander
// copies the grant to each group member. For user grantees, no expansion is needed.
func grantsFromCapabilities(resource *v2.Resource, grantees []*client.GranteeCapabilities, ownSlugMap map[string]string) ([]*v2.Grant, error) {
	var rv []*v2.Grant
	for _, grantee := range grantees {
		for _, capability := range grantee.Capabilities.Capability {
			if capability.Mode != allowMode {
				continue
			}

			ownSlug := slugFromMap(ownSlugMap, capability.Name)

			if grantee.User != nil {
				principalID, err := rs.NewResourceID(resourceTypeUser, grantee.User.ID)
				if err != nil {
					return nil, fmt.Errorf("failed to create user resource ID: %w", err)
				}
				rv = append(rv, grant.NewGrant(resource, ownSlug, principalID))
			}

			if grantee.Group != nil {
				groupID := grantee.Group.ID
				principalID, err := rs.NewResourceID(resourceTypeGroup, groupID)
				if err != nil {
					return nil, fmt.Errorf("failed to create group resource ID: %w", err)
				}
				rv = append(rv, grant.NewGrant(resource, ownSlug, principalID,
					grant.WithAnnotation(&v2.GrantExpandable{
						EntitlementIds: []string{fmt.Sprintf("group:%s:%s", groupID, memberEntitlement)},
						Shallow:        true,
					}),
				))
			}
		}
	}
	return rv, nil
}

// inheritedGrants creates proxy grants expressing that a child resource's permissions
// are inherited from a parent resource. The parent resource is used as the grant
// principal with a GrantExpandable annotation so the SDK expander copies grants
// from the parent entitlement to the child entitlement.
//
// ownSlugMap maps Tableau capability names to display slugs for the child resource's entitlements.
// parentSlugMap maps Tableau capability names to display slugs for the parent resource's entitlements.
func inheritedGrants(resource *v2.Resource, parentResourceType *v2.ResourceType, parentID string, ownSlugMap, parentSlugMap map[string]string) ([]*v2.Grant, error) {
	parentPrincipalID, err := rs.NewResourceID(parentResourceType, parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to create parent principal ID: %w", err)
	}
	parentEntitlementPrefix := parentResourceType.Id + ":" + parentID

	var rv []*v2.Grant
	for capName, ownSlug := range ownSlugMap {
		parentSlug, ok := parentSlugMap[capName]
		if !ok {
			continue
		}
		parentEntitlementID := parentEntitlementPrefix + ":" + parentSlug
		rv = append(rv, grant.NewGrant(resource, ownSlug, parentPrincipalID,
			grant.WithAnnotation(&v2.GrantExpandable{
				EntitlementIds: []string{parentEntitlementID},
				Shallow:        true,
			}),
		))
	}
	return rv, nil
}

// isAlreadyExistsError returns true if the error indicates the resource already exists (HTTP 409).
func isAlreadyExistsError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}

// isNotFoundError returns true if the error indicates the resource was not found (HTTP 404).
func isNotFoundError(err error) bool {
	return status.Code(err) == codes.NotFound
}

// permissionFunc is the signature shared by all Add/Delete permission client methods.
type permissionFunc func(ctx context.Context, resourceID, principalID, capName, capMode string) (annotations.Annotations, error)

// grantPermission handles the common Grant logic for resources that support user and group permissions.
// The display slug parsed from the entitlement ID is converted to a Tableau API capability name
// via capabilityByDisplaySlug before being passed to the permissionFunc. Callers that need custom
// conversion (e.g. default workbook perms) should pass wrapper closures that handle it themselves.
func grantPermission(
	ctx context.Context,
	principal *v2.Resource,
	entitlement *v2.Entitlement,
	addUserPerm permissionFunc,
	addGroupPerm permissionFunc,
) (annotations.Annotations, error) {
	resourceID := entitlement.Resource.Id.Resource
	principalID := principal.Id.Resource
	slug, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	// Convert display slug back to Tableau API capability name.
	capabilityName := slug
	if apiName, ok := capabilityByDisplaySlug[slug]; ok {
		capabilityName = apiName
	}

	var annos annotations.Annotations
	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		annos, err = addUserPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	case resourceTypeGroup.Id:
		annos, err = addGroupPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	default:
		return nil, fmt.Errorf("only users and groups can be granted permissions, got %s", principal.Id.ResourceType)
	}

	if err != nil {
		if isAlreadyExistsError(err) {
			return annotations.New(&v2.GrantAlreadyExists{}), nil
		}
		return annos, fmt.Errorf("failed to grant permission: %w", err)
	}

	return annos, nil
}

// revokePermission handles the common Revoke logic for resources that support user and group permissions.
// The display slug parsed from the entitlement ID is converted to a Tableau API capability name
// via capabilityByDisplaySlug before being passed to the permissionFunc. Callers that need custom
// conversion (e.g. default workbook perms) should pass wrapper closures that handle it themselves.
func revokePermission(
	ctx context.Context,
	g *v2.Grant,
	deleteUserPerm permissionFunc,
	deleteGroupPerm permissionFunc,
) (annotations.Annotations, error) {
	entitlement := g.Entitlement
	principal := g.Principal

	resourceID := entitlement.Resource.Id.Resource
	principalID := principal.Id.Resource
	slug, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	// Convert display slug back to Tableau API capability name.
	capabilityName := slug
	if apiName, ok := capabilityByDisplaySlug[slug]; ok {
		capabilityName = apiName
	}

	var annos annotations.Annotations
	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		annos, err = deleteUserPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	case resourceTypeGroup.Id:
		annos, err = deleteGroupPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	default:
		return nil, fmt.Errorf("only users and groups can have permissions revoked, got %s", principal.Id.ResourceType)
	}

	if err != nil {
		if isNotFoundError(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return annos, fmt.Errorf("failed to revoke permission: %w", err)
	}

	return annos, nil
}

// unsupportedFilterRoles are legacy Tableau Server roles that the REST API
// filter endpoint does not recognize (returns NullPointerException).
var unsupportedFilterRoles = map[string]bool{
	readOnly:          true,
	siteAdministrator: true,
}

// filterableRoles returns only the roles that the Tableau filter API supports.
func filterableRoles(roles []string) []string {
	var result []string
	for _, r := range roles {
		if !unsupportedFilterRoles[r] {
			result = append(result, r)
		}
	}
	return result
}

// siteRoleFilter builds a Tableau API filter expression for the given site roles.
func siteRoleFilter(roles []string) client.ReqOpt {
	if len(roles) == 1 {
		return client.WithFilter(fmt.Sprintf("siteRole:eq:%s", roles[0]))
	}

	return client.WithFilter(fmt.Sprintf("siteRole:in:[%s]", strings.Join(roles, ",")))
}
