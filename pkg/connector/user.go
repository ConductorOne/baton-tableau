package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/conductorone/baton-tableau/pkg/client"
	"google.golang.org/grpc/codes"
)

var _ connectorbuilder.AccountManagerV2 = &userBuilder{}

type userBuilder struct {
	client *client.Client
}

func (u *userBuilder) ResourceType(_ context.Context) *v2.ResourceType {
	return resourceTypeUser
}

// Create a new connector resource for a Tableau user.
func userResource(user *client.User, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	names := strings.SplitN(user.FullName, " ", 2)
	var firstName, lastName string
	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	profile := map[string]any{
		"first_name":   firstName,
		"last_name":    lastName,
		"login":        user.Email,
		"user_id":      user.ID,
		"auth_setting": user.AuthSetting,
	}

	userTraitOptions := []rs.UserTraitOption{
		rs.WithUserProfile(profile),
		rs.WithStatus(v2.UserTrait_Status_STATUS_ENABLED),
		rs.WithEmail(user.Email, true),
	}
	if user.LastLogin != nil {
		userTraitOptions = append(userTraitOptions, rs.WithLastLogin(*user.LastLogin))
	}

	ret, err := rs.NewUserResource(
		user.FullName,
		resourceTypeUser,
		user.ID,
		userTraitOptions,
		rs.WithParentResourceID(parentResourceID),
		rs.WithExternalID(&v2.ExternalId{Id: user.ID}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create user resource for %s: %w", user.ID, err)
	}

	return ret, nil
}

func (u *userBuilder) List(ctx context.Context, parentId *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	if parentId == nil {
		return nil, nil, nil
	}

	users, nextToken, _, err := u.client.GetUsers(ctx, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list users: %w", err)
	}

	var rv []*v2.Resource
	for _, user := range users {
		userResource, err := userResource(user, parentId)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build user resource for %s: %w", user.ID, err)
		}
		rv = append(rv, userResource)
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextToken}, nil
}

func (u *userBuilder) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (u *userBuilder) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (u *userBuilder) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func (u *userBuilder) CreateAccount(ctx context.Context, accountInfo *v2.AccountInfo, credentialOptions *v2.LocalCredentialOptions) (
	connectorbuilder.CreateAccountResponse,
	[]*v2.PlaintextData,
	annotations.Annotations,
	error,
) {
	pMap := accountInfo.Profile.AsMap()
	email, ok := pMap["email"].(string)
	if !ok {
		return nil, nil, nil, fmt.Errorf("email not found in profile")
	}

	siteRole, ok := pMap["siteRole"].(string)
	if !ok {
		return nil, nil, nil, fmt.Errorf("siteRole not found in profile")
	}

	reqBody := client.CreateUserRequest{
		Email:    email,
		SiteRole: siteRole,
	}

	withMFA, _ := pMap["withMFA"].(bool)
	if withMFA {
		reqBody.AuthSetting = "TableauIDWithMFA"
	} else {
		idpConfigName, _ := pMap["idpConfigurationName"].(string)
		idpID, err := u.selectIDPConfiguration(ctx, idpConfigName)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to select IDP configuration: %w", err)
		}
		if idpID != "" {
			reqBody.IdpConfigurationId = idpID
		} else if authSetting, _ := pMap["authSetting"].(string); authSetting != "" {
			// Fallback for environments where site-auth-configurations is unavailable (e.g. older
			// on-premises Tableau Server <2023.3). The caller can set authSetting directly
			// (e.g. "SAML", "OPENID") to control the auth method without IDP discovery.
			reqBody.AuthSetting = authSetting
		}
	}

	user, _, err := u.client.AddUserToSite(ctx, reqBody)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create user %s: %w", email, err)
	}

	resource, err := userResource(user, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse user resource for %s: %w", email, err)
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource: resource,
	}, nil, nil, nil
}

func (u *userBuilder) selectIDPConfiguration(ctx context.Context, idpConfigName string) (string, error) {
	if idpConfigName != "" {
		cfg, _, err := u.client.FindIdpConfigurationByName(ctx, idpConfigName)
		if err != nil {
			return "", fmt.Errorf("failed to find IDP configuration: %w", err)
		}
		if cfg == nil {
			allConfigs, _, listErr := u.client.ListIdpConfigurations(ctx)
			if listErr != nil {
				return "", fmt.Errorf("IDP '%s' not found and failed to list alternatives: %w", idpConfigName, listErr)
			}
			return "", uhttp.WrapErrors(codes.InvalidArgument,
				fmt.Sprintf("IDP configuration '%s' not found", idpConfigName),
				buildAvailableIDPsError(allConfigs))
		}
		return cfg.IdpConfigurationId, nil
	}

	enabledConfigs, _, err := u.client.ListEnabledIdpConfigurations(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list IDP configurations: %w", err)
	}

	switch len(enabledConfigs) {
	case 0:
		return "", nil
	case 1:
		return enabledConfigs[0].IdpConfigurationId, nil
	default:
		return "", uhttp.WrapErrors(codes.InvalidArgument, "multiple IDPs available, specify idpConfigurationName", buildAvailableIDPsError(enabledConfigs))
	}
}

func (u *userBuilder) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	userID := resourceId.Resource
	annos, err := u.client.RemoveUserFromSite(ctx, userID)
	if err != nil {
		return annos, fmt.Errorf("failed to delete user %s: %w", resourceId.Resource, err)
	}

	return annos, nil
}

func newUserBuilder(client *client.Client) *userBuilder {
	return &userBuilder{
		client: client,
	}
}

// buildAvailableIDPsError formats an actionable error listing the given IDP configurations.
func buildAvailableIDPsError(configs []*client.IdpConfiguration) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Please specify idpConfigurationName in the account profile. Available IDPs (%d):\n", len(configs))
	for _, cfg := range configs {
		fmt.Fprintf(&b, "  - %q (ID: %s)\n", cfg.IdpConfigurationName, cfg.IdpConfigurationId)
	}

	return errors.New(b.String())
}
