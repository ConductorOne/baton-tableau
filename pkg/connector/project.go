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
	projectRead  = "Read"
	projectWrite = "Write"
)

var projectCapabilities = map[string]string{
	projectRead:  "read",
	projectWrite: "write",
}

type projectBuilder struct {
	client *client.Client
}

func (p *projectBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeProject
}

func projectResource(project *client.Project, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	ret, err := rs.NewResource(
		project.Name,
		resourceTypeProject,
		project.ID,
		rs.WithParentResourceID(parentResourceID),
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeWorkbook.Id},
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create project resource: %w", err)
	}

	return ret, nil
}

func (p *projectBuilder) List(ctx context.Context, parentId *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	projects, nextToken, _, err := p.client.GetProjects(ctx, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var rv []*v2.Resource
	for _, project := range projects {
		projectResource, err := projectResource(project, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create project resource for %s: %w", project.Name, err)
		}
		rv = append(rv, projectResource)
	}

	return rv, nextToken, nil, nil
}

func (p *projectBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (p *projectBuilder) StaticEntitlements(_ context.Context, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return staticPermissionEntitlements(projectCapabilities, "Project"), "", nil, nil
}

func (p *projectBuilder) Grants(ctx context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	projectID := resource.Id.Resource
	permissions, _, err := p.client.GetProjectPermissions(ctx, projectID)
	if err != nil {
		l.Warn(
			"failed to get project permissions, skipping grants for this project",
			zap.String("project_id", projectID),
			zap.String("project_name", resource.DisplayName),
			zap.Error(err),
		)
		return nil, "", nil, nil
	}

	filtered := filterByCapabilities(permissions, projectCapabilities)
	rv, err := grantsFromCapabilities(resource, filtered)
	if err != nil {
		return nil, "", nil, err
	}

	return rv, "", nil, nil
}

func (p *projectBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	return grantPermission(ctx, principal, entitlement, p.client.AddProjectPermission, p.client.AddProjectGroupPermission)
}

func (p *projectBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	return revokePermission(ctx, g, p.client.DeleteProjectPermission, p.client.DeleteProjectGroupPermission)
}

func newProjectBuilder(client *client.Client) *projectBuilder {
	return &projectBuilder{
		client: client,
	}
}
