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

var workbookCapabilities = map[string]string{
	Read:               "read",
	Write:              "write",
	Filter:             "filter",
	ViewComments:       "view comments",
	AddComment:         "add comment",
	ExportImage:        "export image",
	ExportData:         "export data",
	ShareView:          "share view",
	ViewUnderlyingData: "view underlying data",
	WebAuthoring:       "web authoring",
}

type workbookBuilder struct {
	client       *client.Client
	projectNames map[string]string
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

	projectName, err := w.resolveProjectName(ctx, parentId.Resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to resolve project name for %s: %w", parentId.Resource, err)
	}

	var opts []client.ReqOpt
	if projectName != "" {
		opts = append(opts, client.WithFilter(fmt.Sprintf("projectName:eq:%s", projectName)))
	}

	workbooks, nextToken, _, err := w.client.GetWorkbooks(ctx, pToken.Token, opts...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list workbooks: %w", err)
	}

	var rv []*v2.Resource
	for _, workbook := range workbooks {
		workbookResource, err := workbookResource(workbook, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create workbook resource for %s: %w", workbook.Name, err)
		}
		rv = append(rv, workbookResource)
	}

	return rv, nextToken, nil, nil
}

func (w *workbookBuilder) resolveProjectName(ctx context.Context, projectID string) (string, error) {
	if w.projectNames != nil {
		return w.projectNames[projectID], nil
	}

	projects, err := w.client.GetAllProjects(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list projects: %w", err)
	}

	w.projectNames = make(map[string]string, len(projects))
	for _, p := range projects {
		w.projectNames[p.ID] = p.Name
	}

	return w.projectNames[projectID], nil
}

func (w *workbookBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (w *workbookBuilder) StaticEntitlements(_ context.Context, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return staticPermissionEntitlements(workbookCapabilities, "Workbook"), "", nil, nil
}

func (w *workbookBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	workbookID := resource.Id.Resource
	permissions, _, err := w.client.GetWorkbookPermissions(ctx, workbookID)
	if err != nil {
		l.Warn(
			"failed to get workbook permissions, skipping grants for this workbook",
			zap.String("workbook_id", workbookID),
			zap.String("workbook_name", resource.DisplayName),
			zap.Error(err),
		)
		return nil, "", nil, nil
	}

	filtered := filterByCapabilities(permissions, workbookCapabilities)
	rv, err := grantsFromCapabilities(resource, filtered)
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
	return &workbookBuilder{
		client: client,
	}
}
