package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/tableau"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

const (
	viewRead               = "Read"
	viewFilter             = "Filter"
	viewViewComments       = "ViewComments"
	viewAddComment         = "AddComment"
	viewExportImage        = "ExportImage"
	viewExportData         = "ExportData"
	viewShareView          = "ShareView"
	viewViewUnderlyingData = "ViewUnderlyingData"
	viewWebAuthoring       = "WebAuthoring"
)

var viewCapabilities = map[string]string{
	viewRead:               "view",
	viewFilter:             "filter",
	viewViewComments:       "view comments",
	viewAddComment:         "add comment",
	viewExportImage:        "export image",
	viewExportData:         "export data",
	viewShareView:          "share view",
	viewViewUnderlyingData: "view underlying data",
	viewWebAuthoring:       "web authoring",
}

type viewResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (v *viewResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return v.resourceType
}

func viewResource(view *tableau.View, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	ret, err := rs.NewResource(
		view.Name,
		resourceTypeView,
		view.ID,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to create view resource: %w", err)
	}

	return ret, nil
}

func (v *viewResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	views, err := v.client.GetPaginatedViews(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("tableau-connector: failed to list views: %w", err)
	}

	var rv []*v2.Resource
	for _, view := range views {
		viewCopy := view
		vr, err := viewResource(&viewCopy, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("tableau-connector: failed to create view resource for %s: %w", view.Name, err)
		}
		rv = append(rv, vr)
	}

	return rv, "", nil, nil
}

func (v *viewResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	for capabilityName, displayName := range viewCapabilities {
		permissionOptions := []ent.EntitlementOption{
			ent.WithGrantableTo(resourceTypeUser, resourceTypeGroup),
			ent.WithDescription(fmt.Sprintf("%s permission for %s View", displayName, resource.DisplayName)),
			ent.WithDisplayName(fmt.Sprintf("%s permission %s View", capabilityName, resource.DisplayName)),
		}

		en := ent.NewPermissionEntitlement(resource, capabilityName, permissionOptions...)
		rv = append(rv, en)
	}

	return rv, "", nil, nil
}

func (v *viewResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	var rv []*v2.Grant

	viewID := resource.Id.Resource

	permissions, err := v.client.GetViewPermissions(ctx, viewID)
	if err != nil {
		l.Debug(
			"baton-tableau: failed to get view permissions, skipping grants for this view",
			zap.String("view_id", viewID),
			zap.String("view_name", resource.DisplayName),
			zap.Error(err),
		)
		return rv, "", nil, nil
	}

	for _, grantee := range permissions {
		for _, capability := range grantee.Capabilities.Capability {
			if capability.Mode != "Allow" {
				continue
			}

			if grantee.User != nil {
				principalID, err := rs.NewResourceID(resourceTypeUser, grantee.User.ID)
				if err != nil {
					return nil, "", nil, fmt.Errorf("tableau-connector: failed to create user resource ID: %w", err)
				}
				g := grant.NewGrant(resource, capability.Name, principalID)
				rv = append(rv, g)
			}

			if grantee.Group != nil {
				groupID := grantee.Group.ID
				principalID, err := rs.NewResourceID(resourceTypeGroup, groupID)
				if err != nil {
					return nil, "", nil, fmt.Errorf("tableau-connector: failed to create group resource ID: %w", err)
				}
				g := grant.NewGrant(
					resource,
					capability.Name,
					principalID,
					grant.WithAnnotation(&v2.GrantExpandable{
						EntitlementIds: []string{fmt.Sprintf("group:%s:%s", groupID, memberEntitlement)},
						Shallow:        true,
					}),
				)
				rv = append(rv, g)
			}
		}
	}

	return rv, "", nil, nil
}

func (v *viewResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	viewID := entitlement.Resource.Id.Resource
	principalID := principal.Id.Resource
	capabilityName, err := parseCapabilityFromEntitlementID(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to parse capability from entitlement ID: %w", err)
	}

	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		err = v.client.AddViewPermission(ctx, viewID, principalID, capabilityName, "Allow")
	case resourceTypeGroup.Id:
		err = v.client.AddViewGroupPermission(ctx, viewID, principalID, capabilityName, "Allow")
	default:
		l.Debug(
			"baton-tableau: only users and groups can be granted view permissions",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-tableau: only users and groups can be granted view permissions")
	}

	if err != nil {
		return nil, fmt.Errorf("baton-tableau: failed to grant view permission: %w", err)
	}

	return nil, nil
}

func (v *viewResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	entitlement := grant.Entitlement
	principal := grant.Principal

	viewID := entitlement.Resource.Id.Resource
	principalID := principal.Id.Resource
	capabilityName, err := parseCapabilityFromEntitlementID(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to parse capability from entitlement ID: %w", err)
	}

	switch principal.Id.ResourceType {
	case resourceTypeUser.Id:
		err = v.client.DeleteViewPermission(ctx, viewID, principalID, capabilityName, "Allow")
	case resourceTypeGroup.Id:
		err = v.client.DeleteViewGroupPermission(ctx, viewID, principalID, capabilityName, "Allow")
	default:
		l.Debug(
			"baton-tableau: only users and groups can have view permissions revoked",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-tableau: only users and groups can have view permissions revoked")
	}

	if err != nil {
		return nil, fmt.Errorf("baton-tableau: failed to revoke view permission: %w", err)
	}

	return nil, nil
}

func viewBuilder(client *tableau.Client) *viewResourceType {
	return &viewResourceType{
		resourceType: resourceTypeView,
		client:       client,
	}
}

func parseCapabilityFromEntitlementID(entitlementID string) (string, error) {
	parts := strings.Split(entitlementID, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("tableau-connector: invalid entitlement ID: %s", entitlementID)
	}
	return parts[2], nil
}
