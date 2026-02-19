package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

const (
	siteRoleServerAdmin = "ServerAdministrator"
	allUsersGroupName   = "All Users"
)

// isAllUsersGroup checks if the resource is the Tableau-managed "All Users" group.
// This group auto-includes all site users and cannot be modified via the API.
func isAllUsersGroup(resource *v2.Resource) bool {
	return resource != nil && resource.DisplayName == allUsersGroupName
}

type groupBuilder struct {
	client *client.Client
}

func (g *groupBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeGroup
}

// Create a new connector resource for a Tableau group.
func groupResource(group *client.Group, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]any{
		"group_id":   group.ID,
		"group_name": group.Name,
	}

	groupTraitOptions := []rs.GroupTraitOption{rs.WithGroupProfile(profile)}

	ret, err := rs.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (g *groupBuilder) List(ctx context.Context, parentId *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	groups, nextToken, _, err := g.client.GetGroups(ctx, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list groups: %w", err)
	}

	var rv []*v2.Resource
	for _, group := range groups {
		ur, err := groupResource(&group, parentId)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to build group resource for %s: %w", group.ID, err)
		}
		rv = append(rv, ur)
	}

	return rv, nextToken, nil, nil
}

func (g *groupBuilder) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser),
		ent.WithDescription(fmt.Sprintf("Member of %s Group in Tableau", resource.DisplayName)),
		ent.WithDisplayName(fmt.Sprintf("%s Group %s", resource.DisplayName, memberEntitlement)),
	}

	en := ent.NewAssignmentEntitlement(resource, memberEntitlement, assigmentOptions...)
	rv = append(rv, en)

	return rv, "", nil, nil
}

func (g *groupBuilder) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	groupTrait, err := rs.GetGroupTrait(resource)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to get group trait: %w", err)
	}

	groupId, ok := rs.GetProfileStringValue(groupTrait.Profile, "group_id")
	if !ok {
		return nil, "", nil, fmt.Errorf("missing group_id in group profile")
	}

	users, nextToken, _, err := g.client.GetGroupUsers(ctx, groupId, pToken.Token)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to list group users: %w", err)
	}

	var rv []*v2.Grant
	immutable := isAllUsersGroup(resource)
	l := ctxzap.Extract(ctx)
	for _, user := range users {
		if user.SiteRole == siteRoleServerAdmin {
			l.Debug(
				"skipping server administrator in group membership (server-level admins are not site-scoped users)",
				zap.String("group_id", groupId),
				zap.String("user_id", user.ID),
			)
			continue
		}

		userResource, err := userResource(&user, resource.Id)
		if err != nil {
			return nil, "", nil, fmt.Errorf("failed to build user resource for %s: %w", user.ID, err)
		}

		var grantOpts []grant.GrantOption
		if immutable {
			grantOpts = append(grantOpts, grant.WithAnnotation(&v2.GrantImmutable{}))
		}

		gr := grant.NewGrant(resource, memberEntitlement, userResource.Id, grantOpts...)
		rv = append(rv, gr)
	}

	return rv, nextToken, nil, nil
}

func (g *groupBuilder) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	// "All Users" is Tableau-managed and auto-includes every user. The user is already a member.
	if isAllUsersGroup(entitlement.Resource) {
		l.Debug("skipping grant: All Users group membership is automatic", zap.String("principal_id", principal.Id.Resource))
		return annotations.New(&v2.GrantAlreadyExists{}), nil
	}

	if principal.Id.ResourceType != resourceTypeUser.Id {
		l.Debug(
			"only users can be granted group membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("only users can be granted group membership")
	}

	annos, err := g.client.AddUserToGroup(ctx, entitlement.Resource.Id.Resource, principal.Id.Resource)
	if err != nil {
		if isAlreadyExistsError(err) {
			return annotations.New(&v2.GrantAlreadyExists{}), nil
		}
		return annos, fmt.Errorf("failed to add user to group: %w", err)
	}

	return annos, nil
}

func (g *groupBuilder) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	entitlement := grant.Entitlement
	principal := grant.Principal

	// "All Users" is Tableau-managed. Users cannot be removed from it.
	if isAllUsersGroup(entitlement.Resource) {
		l.Warn("cannot revoke All Users group membership: this group is managed by Tableau and cannot be modified",
			zap.String("principal_id", principal.Id.Resource))
		return nil, fmt.Errorf("cannot revoke membership from the 'All Users' group: this group is automatically managed by Tableau and cannot be modified via the API")
	}

	if principal.Id.ResourceType != resourceTypeUser.Id {
		l.Debug(
			"only users can have group membership revoked",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("only users can have group membership revoked")
	}

	annos, err := g.client.RemoveUserFromGroup(ctx, entitlement.Resource.Id.Resource, principal.Id.Resource)
	if err != nil {
		if isNotFoundError(err) {
			return annotations.New(&v2.GrantAlreadyRevoked{}), nil
		}
		return annos, fmt.Errorf("failed to remove user from group: %w", err)
	}

	return annos, nil
}

func newGroupBuilder(client *client.Client) *groupBuilder {
	return &groupBuilder{
		client: client,
	}
}
