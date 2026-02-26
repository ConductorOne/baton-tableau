package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var projectCapabilities = pickProjectCapabilities(Read, Write)

// projectDefaultWorkbookCapabilities maps Tableau capability names to their display
// slugs for project default-workbook-permission entitlements.
// e.g. "Read" → "Workbook / View", "Filter" → "Workbook / Filter"
var projectDefaultWorkbookCapabilities = func() map[string]string {
	m := make(map[string]string, len(workbookCapabilities))
	for capName, displayName := range workbookCapabilities {
		m[capName] = "Workbook / " + displayName
	}
	return m
}()

// capabilityByWorkbookDefaultSlug is the reverse of projectDefaultWorkbookCapabilities:
// maps "Workbook / View" → "Read", etc.
var capabilityByWorkbookDefaultSlug = func() map[string]string {
	m := make(map[string]string, len(projectDefaultWorkbookCapabilities))
	for capName, slug := range projectDefaultWorkbookCapabilities {
		m[slug] = capName
	}
	return m
}()

// lockedToProjectValues are the Tableau contentPermissions values that indicate
// workbook/view permissions are locked to (inherited from) the project.
var lockedToProjectValues = map[string]bool{
	"LockedToProject":              true,
	"LockedToProjectWithoutNested": true,
}

func isLockedToProject(contentPermissions string) bool {
	return lockedToProjectValues[contentPermissions]
}

// isDefaultWorkbookSlug reports whether the entitlement slug refers to a
// project default workbook permission (e.g. "Workbook / View").
func isDefaultWorkbookSlug(slug string) bool {
	_, ok := capabilityByWorkbookDefaultSlug[slug]
	return ok
}

type projectBuilder struct {
	client *client.Client
}

func (p *projectBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeProject
}

func projectResource(project *client.Project) (*v2.Resource, error) {
	var parentResourceID *v2.ResourceId
	if project.ParentProjectId != "" {
		parentResourceID = &v2.ResourceId{
			ResourceType: resourceTypeProject.Id,
			Resource:     project.ParentProjectId,
		}
	}

	ret, err := rs.NewResource(
		project.Name,
		resourceTypeProject,
		project.ID,
		rs.WithParentResourceID(parentResourceID),
		rs.WithAnnotation(
			&v2.ChildResourceType{ResourceTypeId: resourceTypeWorkbook.Id},
			wrapperspb.String(project.ContentPermissions),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create project resource: %w", err)
	}

	return ret, nil
}

// projectContentPermissions reads the ContentPermissions value stored in the
// project resource annotations during List.
func projectContentPermissions(resource *v2.Resource) string {
	for _, ann := range resource.Annotations {
		var cp wrapperspb.StringValue
		if ann.MessageIs(&cp) {
			if err := ann.UnmarshalTo(&cp); err == nil {
				return cp.Value
			}
		}
	}
	return ""
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
		resource, err := projectResource(project)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to create project resource for %s: %w", project.Name, err)
		}
		rv = append(rv, resource)
	}

	return rv, nextToken, nil, nil
}

func (p *projectBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (p *projectBuilder) StaticEntitlements(_ context.Context, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	rv := staticPermissionEntitlements(projectCapabilities, "Project", resourceTypeProject)
	rv = append(rv, staticPermissionEntitlements(projectDefaultWorkbookCapabilities, "Project", resourceTypeProject)...)
	return rv, "", nil, nil
}

func (p *projectBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var b pagination.Bag
	if err := b.Unmarshal(pToken.Token); err != nil {
		return nil, "", nil, err
	}

	projectID := resource.Id.Resource

	switch b.ResourceTypeID() {
	case "":
		// Phase 1: fetch project permissions and default workbook permissions.
		accessPerms, _, err := p.client.GetProjectPermissions(ctx, projectID)
		if err != nil {
			return nil, "", nil, err
		}

		rv, err := grantsFromCapabilities(resource, accessPerms, projectCapabilities)
		if err != nil {
			return nil, "", nil, err
		}

		defaultWorkbookPerms, _, err := p.client.GetProjectDefaultWorkbookPermissions(ctx, projectID)
		if err != nil {
			return rv, "", nil, err
		}

		defaultWorkbookRv, err := grantsFromCapabilities(resource, defaultWorkbookPerms, projectDefaultWorkbookCapabilities)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, defaultWorkbookRv...)

		// For nested projects, create proxy grants so the SDK expander propagates
		// the parent project's default workbook permissions to this project.
		if resource.ParentResourceId != nil && resource.ParentResourceId.ResourceType == resourceTypeProject.Id {
			parentID := resource.ParentResourceId.Resource
			proxyRv, err := inheritedGrants(resource, resourceTypeProject, parentID, projectDefaultWorkbookCapabilities, projectDefaultWorkbookCapabilities)
			if err != nil {
				return nil, "", nil, err
			}
			rv = append(rv, proxyRv...)
		}

		// If the project is locked, kick off workbook iteration in the next page.
		if isLockedToProject(projectContentPermissions(resource)) {
			b.Push(pagination.PageState{
				ResourceTypeID: resourceTypeWorkbook.Id,
				Token:          "",
			})
		}

		nextToken, err := b.Marshal()
		if err != nil {
			return nil, "", nil, err
		}
		return rv, nextToken, nil, nil

	case resourceTypeWorkbook.Id:
		// Phase 2: emit inherited grants for every workbook belonging to this
		// locked project, paginating through all workbooks.
		workbooks, nextWorkbookToken, _, err := p.client.GetWorkbooks(ctx, b.PageToken())
		if err != nil {
			return nil, "", nil, err
		}

		var rv []*v2.Grant
		for _, workbook := range workbooks {
			if workbook.Project == nil || workbook.Project.ID != projectID {
				continue
			}
			wResource, err := workbookResource(workbook, resource.Id)
			if err != nil {
				return nil, "", nil, fmt.Errorf("failed to create workbook resource for %s: %w", workbook.Name, err)
			}
			wGrants, err := inheritedGrants(wResource, resourceTypeProject, projectID, workbookCapabilities, projectDefaultWorkbookCapabilities)
			if err != nil {
				return nil, "", nil, err
			}
			rv = append(rv, wGrants...)
		}

		if err := b.Next(nextWorkbookToken); err != nil {
			return nil, "", nil, err
		}
		nextToken, err := b.Marshal()
		if err != nil {
			return nil, "", nil, err
		}
		return rv, nextToken, nil, nil
	}

	return nil, "", nil, nil
}

func (p *projectBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	slug, err := parseEntitlementSlug(entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	if isDefaultWorkbookSlug(slug) {
		// The slug is a display name like "Workbook / View"; convert to Tableau API capability name.
		addUser := func(ctx context.Context, projectID, userID, capSlug, mode string) (annotations.Annotations, error) {
			capName := capabilityByWorkbookDefaultSlug[capSlug]
			return p.client.AddProjectDefaultWorkbookPermission(ctx, projectID, userID, capName, mode)
		}
		addGroup := func(ctx context.Context, projectID, groupID, capSlug, mode string) (annotations.Annotations, error) {
			capName := capabilityByWorkbookDefaultSlug[capSlug]
			return p.client.AddProjectDefaultWorkbookGroupPermission(ctx, projectID, groupID, capName, mode)
		}
		return grantPermission(ctx, principal, entitlement, addUser, addGroup)
	}

	return grantPermission(ctx, principal, entitlement, p.client.AddProjectPermission, p.client.AddProjectGroupPermission)
}

func (p *projectBuilder) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	slug, err := parseEntitlementSlug(g.Entitlement.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to parse capability from entitlement ID: %w", err)
	}

	if isDefaultWorkbookSlug(slug) {
		// The slug is a display name like "Workbook / View"; convert to Tableau API capability name.
		deleteUser := func(ctx context.Context, projectID, userID, capSlug, mode string) (annotations.Annotations, error) {
			capName := capabilityByWorkbookDefaultSlug[capSlug]
			return p.client.DeleteProjectDefaultWorkbookPermission(ctx, projectID, userID, capName, mode)
		}
		deleteGroup := func(ctx context.Context, projectID, groupID, capSlug, mode string) (annotations.Annotations, error) {
			capName := capabilityByWorkbookDefaultSlug[capSlug]
			return p.client.DeleteProjectDefaultWorkbookGroupPermission(ctx, projectID, groupID, capName, mode)
		}
		return revokePermission(ctx, g, deleteUser, deleteGroup)
	}

	return revokePermission(ctx, g, p.client.DeleteProjectPermission, p.client.DeleteProjectGroupPermission)
}

func newProjectBuilder(client *client.Client) *projectBuilder {
	return &projectBuilder{client: client}
}
