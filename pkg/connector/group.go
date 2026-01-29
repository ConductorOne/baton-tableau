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
	"github.com/conductorone/baton-tableau/pkg/tableau"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

const (
	memberEntitlement   = "member"
	siteRoleServerAdmin = "ServerAdministrator"
)

type groupResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (g *groupResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return g.resourceType
}

// Create a new connector resource for a Tableau group.
func groupResource(group *tableau.Group, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
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

func (g *groupResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	groups, err := g.client.GetPaginatedGroups(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("tableau-connector: failed to list groups: %w", err)
	}

	var rv []*v2.Resource
	for _, group := range groups {
		groupCopy := group
		ur, err := groupResource(&groupCopy, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, ur)
	}

	return rv, "", nil, nil
}

func (g *groupResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
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

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant

	groupTrait, err := rs.GetGroupTrait(resource)
	if err != nil {
		return nil, "", nil, err
	}

	groupId, ok := rs.GetProfileStringValue(groupTrait.Profile, "group_id")
	if !ok {
		return nil, "", nil, fmt.Errorf("error fetching group_id from group profile")
	}

	users, err := g.client.GetPaginatedGroupUsers(ctx, groupId)
	if err != nil {
		return nil, "", nil, err
	}

	l := ctxzap.Extract(ctx)
	for _, user := range users {
		if user.SiteRole == siteRoleServerAdmin {
			l.Debug(
				"baton-tableau: skipping server administrator in group membership (server-level admins are not site-scoped users)",
				zap.String("group_id", groupId),
				zap.String("user_id", user.ID),
				zap.String("user_email", user.Email),
			)
			continue
		}

		userCopy := user
		ur, err := userResource(&userCopy, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		grant := grant.NewGrant(resource, memberEntitlement, ur.Id)
		rv = append(rv, grant)
	}

	return rv, "", nil, nil
}

func (o *groupResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	if principal.Id.ResourceType != resourceTypeUser.Id {
		l.Warn(
			"baton-tableau: only users can be granted group membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-tableau: only users can be granted group membership")
	}

	err := o.client.AddUserToGroup(ctx, entitlement.Resource.Id.Resource, principal.Id.Resource)
	if err != nil {
		return nil, fmt.Errorf("baton-tableau: failed to add user to group: %w", err)
	}

	return nil, nil
}

func (o *groupResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	entitlement := grant.Entitlement
	principal := grant.Principal

	if principal.Id.ResourceType != resourceTypeUser.Id {
		l.Warn(
			"baton-tableau: only users can have group membership revoked",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-tableau: only users can have group membership revoked")
	}

	err := o.client.RemoveUserFromGroup(ctx, entitlement.Resource.Id.Resource, principal.Id.Resource)
	if err != nil {
		return nil, fmt.Errorf("baton-tableau: failed to remove user from group: %w", err)
	}

	return nil, nil
}

func groupBuilder(client *tableau.Client) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       client,
	}
}
