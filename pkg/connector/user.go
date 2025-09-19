package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/tableau"
)

type userResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (o *userResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// Create a new connector resource for a Tableau user.
func userResource(ctx context.Context, user *tableau.User, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	names := strings.SplitN(user.FullName, " ", 2)
	var firstName, lastName string
	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	profile := map[string]interface{}{
		"first_name": firstName,
		"last_name":  lastName,
		"login":      user.Email,
		"user_id":    user.ID,
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithEmail(user.Email, true),
		rs.WithStatus(v2.UserTrait_Status_STATUS_ENABLED),
		rs.WithLastLogin(user.LastLogin),
	}

	ret, err := rs.NewUserResource(
		user.FullName,
		resourceTypeUser,
		user.ID,
		userTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *userResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	if parentId == nil {
		return nil, "", nil, nil
	}

	users, err := o.client.GetPaginatedUsers(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	var rv []*v2.Resource
	for _, user := range users {
		userCopy := user
		ur, err := userResource(ctx, &userCopy, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, ur)
	}

	return rv, "", nil, nil
}

func (o *userResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (o *userResourceType) Grants(_ context.Context, _ *v2.Resource, _ *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (u *userResourceType) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_RANDOM_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func (o *userResourceType) CreateAccount(ctx context.Context, accountInfo *v2.AccountInfo, credentialOptions *v2.CredentialOptions) (
	connectorbuilder.CreateAccountResponse,
	[]*v2.PlaintextData,
	annotations.Annotations,
	error,
) {
	pMap := accountInfo.Profile.AsMap()
	email, ok := pMap["email"].(string)
	if !ok {
		return nil, nil, nil, fmt.Errorf("baton-tableau: name not found in profile")
	}

	siteRole, ok := pMap["siteRole"].(string)
	if !ok {
		return nil, nil, nil, fmt.Errorf("baton-tableau: siteRole not found in profile")
	}

	err := o.client.AddUserToSite(ctx, tableau.CreateUserRequest{
		Email:    email,
		SiteRole: siteRole,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("baton-tableau: failed to create user %s: %w", email, err)
	}

	return &v2.CreateAccountResponse_ActionRequiredResult{
		Message:               fmt.Sprintf("User %s invitation sent successfully", email),
		IsCreateAccountResult: true,
	}, nil, nil, nil
}

func (o *userResourceType) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	userID := resourceId.Resource
	err := o.client.RemoveUserFromSite(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("baton-tableau: failed to delete user %s: %w", resourceId.Resource, err)
	}

	return nil, nil
}

func userBuilder(client *tableau.Client) *userResourceType {
	return &userResourceType{
		resourceType: resourceTypeUser,
		client:       client,
	}
}
