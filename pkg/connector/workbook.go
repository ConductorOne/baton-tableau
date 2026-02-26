package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
)

var workbookCapabilities = pickCapabilities(
	Read,
	Filter,
	ViewComments,
	AddComment,
	ExportImage,
	ExportData,
	ShareView,
	ViewUnderlyingData,
	WebAuthoring,
	RunExplainData,
	ExportXml,
	Write,
	Delete,
	ChangePermissions,
	ChangeHierarchy,
)

type workbookBuilder struct {
	client *client.Client
}

func (w *workbookBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeWorkbook
}

func workbookResource(workbook *client.Workbook, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	ret, err := rs.NewResource(
		workbook.Name,
		resourceTypeWorkbook,
		workbook.ID,
		rs.WithParentResourceID(parentResourceID),
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeView.Id},
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create workbook resource: %w", err)
	}

	return ret, nil
}

func (w *workbookBuilder) List(ctx context.Context, parentId *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	projectID := parentId.Resource
	workbooks, nextToken, _, err := w.client.GetWorkbooks(ctx, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list workbooks: %w", err)
	}

	var rv []*v2.Resource
	for _, workbook := range workbooks {
		if workbook.Project == nil || workbook.Project.ID != projectID {
			continue
		}
		resource, err := workbookResource(workbook, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create workbook resource for %s: %w", workbook.Name, err)
		}
		rv = append(rv, resource)
	}

	return rv, nextToken, nil, nil
}

func (w *workbookBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (w *workbookBuilder) StaticEntitlements(_ context.Context, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return staticPermissionEntitlements(workbookCapabilities, "Workbook", resourceTypeProject), "", nil, nil
}

func (w *workbookBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	workbookID := resource.Id.Resource
	permissions, _, err := w.client.GetWorkbookPermissions(ctx, workbookID)
	if err != nil {
		return nil, "", nil, err
	}

	rv, err := grantsFromCapabilities(resource, permissions, workbookCapabilities)
	if err != nil {
		return nil, "", nil, err
	}

	return rv, "", nil, nil
}

func (w *workbookBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	return grantPermission(ctx, principal, entitlement, w.client.AddWorkbookPermission, w.client.AddWorkbookGroupPermission)
}

func (w *workbookBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	return revokePermission(ctx, g, w.client.DeleteWorkbookPermission, w.client.DeleteWorkbookGroupPermission)
}

func newWorkbookBuilder(client *client.Client) *workbookBuilder {
	return &workbookBuilder{client: client}
}
