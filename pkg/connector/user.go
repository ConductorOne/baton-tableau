package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/conductorone/baton-tableau/pkg/tableau"
	"google.golang.org/grpc/codes"
)

var _ connectorbuilder.AccountManager = &userResourceType{}

type userResourceType struct {
	resourceType *v2.ResourceType
	client       *tableau.Client
}

func (o *userResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// Create a new connector resource for a Tableau user.
func userResource(user *tableau.User, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
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
		"first_name":   firstName,
		"last_name":    lastName,
		"login":        user.Email,
		"user_id":      user.ID,
		"auth_setting": user.AuthSetting,
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
		ur, err := userResource(&userCopy, parentId)
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
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func (o *userResourceType) CreateAccount(ctx context.Context, accountInfo *v2.AccountInfo, credentialOptions *v2.LocalCredentialOptions) (
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

	var idpID string
	var err error
	// Tableau API defaults to MFA authentication when IdpConfigurationId is empty.
	// withMFA=true: Leave IdpConfigurationId empty to use Tableau's MFA default.
	// withMFA=false: Select a SAML IDP configuration from the available IDPs.
	withMFA, _ := pMap["withMFA"].(bool)
	if !withMFA {
		idpID, err = o.selectIDPConfiguration(ctx, pMap)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("baton-tableau: failed to select IDP configuration: %w", err)
		}
	}

	reqBody := tableau.CreateUserRequest{
		Email:    email,
		SiteRole: siteRole,
	}
	if idpID != "" {
		reqBody.IdpConfigurationId = idpID
	}

	user, err := o.client.AddUserToSite(ctx, reqBody)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("baton-tableau: failed to create user %s: %w", email, err)
	}

	resource, err := userResource(user, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("baton-tableau: failed to parse user resource for %s: %w", email, err)
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource: resource,
	}, nil, nil, nil
}

func (o *userResourceType) selectIDPConfiguration(ctx context.Context, pMap map[string]interface{}) (string, error) {
	idpConfigName, _ := pMap["idpConfigurationName"].(string)

	idpConfigs, err := o.client.ListIdpConfigurations(ctx)
	if err != nil {
		return "", fmt.Errorf("baton-tableau: failed to list IDP configurations: %w", err)
	}

	var enabledSAMLConfigs []tableau.IdpConfiguration
	for i := range idpConfigs {
		if idpConfigs[i].AuthSetting == "SAML" && idpConfigs[i].Enabled {
			enabledSAMLConfigs = append(enabledSAMLConfigs, idpConfigs[i])
		}
	}

	if len(enabledSAMLConfigs) == 0 {
		return "", fmt.Errorf("baton-tableau: you need to pass the MFA flag since no IDP is configured in Tableau")
	}

	if len(enabledSAMLConfigs) == 1 {
		return enabledSAMLConfigs[0].IdpConfigurationId, nil
	}

	if idpConfigName == "" {
		return "", uhttp.WrapErrors(codes.InvalidArgument, "multiple SAML IDPs available", buildMultipleIDPError(enabledSAMLConfigs))
	}

	selectedConfig, err := findIDPByName(enabledSAMLConfigs, idpConfigName)
	if err != nil {
		return "", uhttp.WrapErrors(codes.InvalidArgument, fmt.Sprintf("IDP configuration '%s' not found", idpConfigName), buildMultipleIDPError(enabledSAMLConfigs))
	}

	return selectedConfig.IdpConfigurationId, nil
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

func findIDPByName(configs []tableau.IdpConfiguration, name string) (*tableau.IdpConfiguration, error) {
	lowerName := strings.ToLower(name)
	for i := range configs {
		if strings.ToLower(configs[i].IdpConfigurationName) == lowerName {
			return &configs[i], nil
		}
	}
	return nil, fmt.Errorf("IDP configuration with name '%s' not found", name)
}

func buildMultipleIDPError(configs []tableau.IdpConfiguration) error {
	msg := fmt.Sprintf(`baton-tableau: multiple SAML IDP configurations found (%d available). Please specify idpConfigurationName in the account profile. Available IDPs:
`, len(configs))

	for _, config := range configs {
		msg += fmt.Sprintf("  - \"%s\" (ID: %s)\n", config.IdpConfigurationName, config.IdpConfigurationId)
	}

	return errors.New(msg)
}
