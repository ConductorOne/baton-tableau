package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
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

type viewBuilder struct {
	client *client.Client
}

func (v *viewBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeView
}

// getShowTabs checks whether a workbook has showTabs enabled by calling the
// Tableau API directly. When showTabs is true, view permissions are inherited
// from the workbook and cannot be managed individually.
func (v *viewBuilder) getShowTabs(ctx context.Context, workbookID string) (bool, error) {
	workbook, _, err := v.client.GetWorkbook(ctx, workbookID)
	if err != nil {
		return false, fmt.Errorf("failed to get workbook %s: %w", workbookID, err)
	}

	return workbook.ShowTabs == "true", nil
}

func viewResource(view *client.View, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	ret, err := rs.NewResource(
		view.Name,
		resourceTypeView,
		view.ID,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create view resource: %w", err)
	}

	return ret, nil
}

func (v *viewBuilder) List(ctx context.Context, parentId *v2.ResourceId, _ *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	workbookID := parentId.Resource
	views, _, err := v.client.GetWorkbookViews(ctx, workbookID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list views for workbook %s: %w", workbookID, err)
	}

	var rv []*v2.Resource
	for _, view := range views {
		vr, err := viewResource(&view, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create view resource for %s: %w", view.Name, err)
		}
		rv = append(rv, vr)
	}

	return rv, "", nil, nil
}

func (v *viewBuilder) Entitlements(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	if resource.ParentResourceId != nil {
		showTabs, err := v.getShowTabs(ctx, resource.ParentResourceId.Resource)
		if err != nil {
			return nil, "", nil, err
		}
		if showTabs {
			return nil, "", nil, nil
		}
	}

	return permissionEntitlements(resource, viewCapabilities, "View"), "", nil, nil
}

func (v *viewBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	if resource.ParentResourceId != nil {
		showTabs, err := v.getShowTabs(ctx, resource.ParentResourceId.Resource)
		if err != nil {
			return nil, "", nil, err
		}
		if showTabs {
			return nil, "", nil, nil
		}
	}

	viewID := resource.Id.Resource
	permissions, _, err := v.client.GetViewPermissions(ctx, viewID)
	if err != nil {
		l.Warn(
			"failed to get view permissions, skipping grants for this view",
			zap.String("view_id", viewID),
			zap.String("view_name", resource.DisplayName),
			zap.Error(err),
		)
		return nil, "", nil, nil
	}

	rv, err := grantsFromCapabilities(resource, permissions)
	if err != nil {
		return nil, "", nil, err
	}

	return rv, "", nil, nil
}

func (v *viewBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	if entitlement.Resource.ParentResourceId != nil {
		showTabs, err := v.getShowTabs(ctx, entitlement.Resource.ParentResourceId.Resource)
		if err != nil {
			return nil, err
		}
		if showTabs {
			return nil, fmt.Errorf("cannot grant view permission: parent workbook has showTabs enabled, permissions are inherited from the workbook")
		}
	}

	return grantPermission(ctx, principal, entitlement, v.client.AddViewPermission, v.client.AddViewGroupPermission)
}

func (v *viewBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	if g.Entitlement.Resource.ParentResourceId != nil {
		showTabs, err := v.getShowTabs(ctx, g.Entitlement.Resource.ParentResourceId.Resource)
		if err != nil {
			return nil, err
		}
		if showTabs {
			return nil, fmt.Errorf("cannot revoke view permission: parent workbook has showTabs enabled, permissions are inherited from the workbook")
		}
	}

	return revokePermission(ctx, g, v.client.DeleteViewPermission, v.client.DeleteViewGroupPermission)
}

func newViewBuilder(client *client.Client) *viewBuilder {
	return &viewBuilder{
		client: client,
	}
}
