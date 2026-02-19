package connector

import (
	"context"
	"fmt"
	"slices"
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
)

// parseEntitlementSlug extracts the slug (last segment) from an entitlement ID.
// Entitlement IDs have the format "resourceType:resourceId:slug".
func parseEntitlementSlug(entitlementID string) (string, error) {
	parts := strings.Split(entitlementID, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid entitlement ID: %s", entitlementID)
	}
	return parts[2], nil
}

// permissionEntitlements creates permission entitlements for a resource from a capabilities map.
// Keys are sorted to ensure deterministic output order.
func permissionEntitlements(resource *v2.Resource, capabilities map[string]string, resourceLabel string) []*v2.Entitlement {
	keys := make([]string, 0, len(capabilities))
	for k := range capabilities {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var rv []*v2.Entitlement
	for _, capabilityName := range keys {
		displayName := capabilities[capabilityName]
		permissionOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser, resourceTypeGroup),
			ent.WithDescription(fmt.Sprintf("%s permission for %s %s", displayName, resource.DisplayName, resourceLabel)),
			ent.WithDisplayName(fmt.Sprintf("%s permission %s %s", capabilityName, resource.DisplayName, resourceLabel)),
		}
		entitlement := ent.NewPermissionEntitlement(resource, capabilityName, permissionOptions...)
		rv = append(rv, entitlement)
	}
	return rv
}

// grantsFromCapabilities creates grants from Tableau grantee capabilities.
func grantsFromCapabilities(resource *v2.Resource, grantees []client.GranteeCapabilities) ([]*v2.Grant, error) {
	var rv []*v2.Grant
	for _, grantee := range grantees {
		for _, capability := range grantee.Capabilities.Capability {
			if capability.Mode != allowMode {
				continue
			}

			if grantee.User != nil {
				principalID, err := rs.NewResourceID(resourceTypeUser, grantee.User.ID)
				if err != nil {
					return nil, fmt.Errorf("failed to create user resource ID: %w", err)
				}
				grant := grant.NewGrant(resource, capability.Name, principalID)
				rv = append(rv, grant)
			}

			if grantee.Group != nil {
				groupID := grantee.Group.ID
				principalID, err := rs.NewResourceID(resourceTypeGroup, groupID)
				if err != nil {
					return nil, fmt.Errorf("failed to create group resource ID: %w", err)
				}
				grant := grant.NewGrant(
					resource,
					capability.Name,
					principalID,
					grant.WithAnnotation(&v2.GrantExpandable{
						EntitlementIds: []string{fmt.Sprintf("group:%s:%s", groupID, memberEntitlement)},
						Shallow:        true,
					}),
				)
				rv = append(rv, grant)
			}
		}
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
func grantPermission(
	ctx context.Context,
	principal *v2.Resource,
	entitlement *v2.Entitlement,
	addUserPerm permissionFunc,
	addGroupPerm permissionFunc,
) (annotations.Annotations, error) {
	resourceID := entitlement.Resource.Id.Resource
	principalID := principal.Id.Resource
	capabilityName, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	var annos annotations.Annotations
	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		annos, err = addUserPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	case resourceTypeGroup.Id:
		annos, err = addGroupPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	default:
		return nil, fmt.Errorf("baton-tableau: only users and groups can be granted permissions, got %s", principal.Id.ResourceType)
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
	capabilityName, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	var annos annotations.Annotations
	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		annos, err = deleteUserPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	case resourceTypeGroup.Id:
		annos, err = deleteGroupPerm(ctx, resourceID, principalID, capabilityName, allowMode)
	default:
		return nil, fmt.Errorf("baton-tableau: only users and groups can have permissions revoked, got %s", principal.Id.ResourceType)
	}

	if err != nil {
		if isNotFoundError(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return annos, fmt.Errorf("failed to revoke permission: %w", err)
	}

	return annos, nil
}
