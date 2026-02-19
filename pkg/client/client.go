// Package client provides a client for the Tableau REST API.
//
// API Endpoints Used:
//
//	Authentication:
//	- POST /api/{version}/auth/signin                                                           - Sign in with Personal Access Token
//
//	Sites:
//	- GET  /api/{version}/sites/{siteId}                                                        - Get site details
//
//	Users:
//	- GET    /api/{version}/sites/{siteId}/users                                                - List users (paginated)
//	- GET    /api/{version}/sites/{siteId}/users/{userId}                                       - Verify current user
//	- POST   /api/{version}/sites/{siteId}/users                                                - Add user to site
//	- PUT    /api/{version}/sites/{siteId}/users/{userId}                                       - Update user site role
//	- DELETE /api/{version}/sites/{siteId}/users/{userId}                                       - Remove user from site
//
//	Groups:
//	- GET    /api/{version}/sites/{siteId}/groups                                               - List groups (paginated)
//	- GET    /api/{version}/sites/{siteId}/groups/{groupId}/users                               - List group members (paginated)
//	- POST   /api/{version}/sites/{siteId}/groups/{groupId}/users                               - Add user to group
//	- DELETE /api/{version}/sites/{siteId}/groups/{groupId}/users/{userId}                      - Remove user from group
//
//	Projects:
//	- GET    /api/{version}/sites/{siteId}/projects                                             - List projects (paginated)
//	- GET    /api/{version}/sites/{siteId}/projects/{projectId}/permissions                     - Get project permissions
//	- PUT    /api/{version}/sites/{siteId}/projects/{projectId}/permissions                     - Add project permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/permissions/users/{userId}/{cap}/{mode}   - Delete user project permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/permissions/groups/{groupId}/{cap}/{mode} - Delete group project permission
//
//	Workbooks:
//	- GET    /api/{version}/sites/{siteId}/workbooks                                            - List workbooks (paginated)
//	- GET    /api/{version}/sites/{siteId}/workbooks/{workbookId}                               - Get workbook details
//	- GET    /api/{version}/sites/{siteId}/workbooks/{workbookId}/views                         - List workbook views
//	- GET    /api/{version}/sites/{siteId}/workbooks/{workbookId}/permissions                   - Get workbook permissions
//	- PUT    /api/{version}/sites/{siteId}/workbooks/{workbookId}/permissions                   - Add workbook permission
//	- DELETE /api/{version}/sites/{siteId}/workbooks/{workbookId}/permissions/users/{userId}/{cap}/{mode}   - Delete user workbook permission
//	- DELETE /api/{version}/sites/{siteId}/workbooks/{workbookId}/permissions/groups/{groupId}/{cap}/{mode} - Delete group workbook permission
//
//	Views:
//	- GET    /api/{version}/sites/{siteId}/views                                                - List views (paginated)
//	- GET    /api/{version}/sites/{siteId}/views/{viewId}/permissions                           - Get view permissions
//	- PUT    /api/{version}/sites/{siteId}/views/{viewId}/permissions                           - Add view permission
//	- DELETE /api/{version}/sites/{siteId}/views/{viewId}/permissions/users/{userId}/{cap}/{mode}   - Delete user view permission
//	- DELETE /api/{version}/sites/{siteId}/views/{viewId}/permissions/groups/{groupId}/{cap}/{mode} - Delete group view permission
//
//	IDP Configurations:
//	- GET  /api/{version}/sites/{siteId}/site-auth-configurations                               - List IDP configurations
//
// Authentication:
//   - Personal Access Token (PAT) via /auth/signin, then X-Tableau-Auth header
//
// Pagination:
//   - Uses pageSize and pageNumber query parameters (1-based, default pageSize=100)
//   - Responses include pagination object with pageNumber, pageSize, totalAvailable
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

// =============================================================================
// Client
// =============================================================================

// Client is an HTTP client for the Tableau REST API.
type Client struct {
	httpClient *uhttp.BaseHttpClient
	authToken  string
	siteId     string
	baseUrl    string
}

// New creates a fully initialized Tableau API client: builds the base URL,
// sets up HTTP transport, authenticates, and returns a ready-to-use client.
func New(ctx context.Context, serverPath, siteID, accessTokenName, accessTokenSecret, apiVersion string) (*Client, error) {
	baseURL, err := BuildBaseURL(serverPath, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to build base URL: %w", err)
	}

	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	baseHttpClient, err := uhttp.NewBaseHttpClientWithContext(ctx, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create base http client: %w", err)
	}

	credentials, err := Login(ctx, baseHttpClient, baseURL, siteID, accessTokenSecret, accessTokenName)
	if err != nil {
		return nil, fmt.Errorf("failed to login: %w", err)
	}

	return &Client{
		httpClient: baseHttpClient,
		authToken:  credentials.Token,
		siteId:     credentials.Site.ID,
		baseUrl:    baseURL,
	}, nil
}

// =============================================================================
// Authentication
// =============================================================================

// Login authenticates with a Personal Access Token and returns session credentials.
func Login(ctx context.Context, httpClient *uhttp.BaseHttpClient, baseUrl, contentUrl, accessToken, tokenName string) (*Credentials, error) {
	loginURL, err := buildURL(baseUrl, authSignin)
	if err != nil {
		return nil, fmt.Errorf("failed to build login URL: %w", err)
	}

	body := map[string]any{
		"credentials": map[string]any{
			"personalAccessTokenName":   tokenName,
			"personalAccessTokenSecret": accessToken,
			"site": map[string]string{
				"contentUrl": contentUrl,
			},
		},
	}

	req, err := httpClient.NewRequest(ctx, http.MethodPost, loginURL,
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithJSONBody(body),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create login request: %w", err)
	}

	var res credentialsResponse
	resp, err := httpClient.Do(req, uhttp.WithJSONResponse(&res))
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}
	defer resp.Body.Close()

	return &res.Credentials, nil
}

// =============================================================================
// Site API
// =============================================================================

// GetSite returns site details of the site the user is logged in to.
func (c *Client) GetSite(ctx context.Context) (*Site, annotations.Annotations, error) {
	urlGetSite, err := c.buildSiteURL()
	if err != nil {
		return nil, nil, err
	}

	var res siteResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetSite, &res, nil)
	if err != nil {
		return nil, nil, err
	}

	return &res.Site, annos, nil
}

// =============================================================================
// Users API
// =============================================================================

// GetUsers returns a page of users on the site.
func (c *Client) GetUsers(ctx context.Context, pageToken string) ([]User, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetUsers, err := c.buildSiteURL(pathUsers)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetUsers, withPagination(page))

	var res usersResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetUsers, &res, nil)
	if err != nil {
		return nil, "", annos, err
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Users.User, nextToken, annos, nil
}

// AddUserToSite creates a new user on the site.
func (c *Client) AddUserToSite(ctx context.Context, user CreateUserRequest) (*User, annotations.Annotations, error) {
	urlAddUserToSite, err := c.buildSiteURL(pathUsers)
	if err != nil {
		return nil, nil, err
	}

	userMap := map[string]any{
		"name":     user.Email,
		"siteRole": user.SiteRole,
	}
	if user.IdpConfigurationId != "" {
		userMap["idpConfigurationId"] = user.IdpConfigurationId
	}

	var res createUserResponse
	userBody := map[string]any{pathUser: userMap}
	annos, err := c.doRequest(ctx, http.MethodPost, urlAddUserToSite, &res, userBody)
	if err != nil {
		return nil, nil, err
	}

	if res.User == nil {
		return nil, nil, fmt.Errorf("failed to create user, no user returned")
	}

	return res.User, annos, nil
}

// UpdateUserSiteRole updates a user's site role.
func (c *Client) UpdateUserSiteRole(ctx context.Context, userId, siteRole string) (annotations.Annotations, error) {
	urlUpdateUserSiteRole, err := c.buildSiteURL(pathUsers, userId)
	if err != nil {
		return nil, err
	}

	var res userResponse
	userBody := map[string]any{pathUser: map[string]any{"siteRole": siteRole}}
	annos, err := c.doRequest(ctx, http.MethodPut, urlUpdateUserSiteRole, &res, userBody)
	if err != nil {
		return nil, err
	}

	return annos, nil
}

// RemoveUserFromSite removes a user from the site.
func (c *Client) RemoveUserFromSite(ctx context.Context, userId string) (annotations.Annotations, error) {
	urlRemoveUserFromSite, err := c.buildSiteURL(pathUsers, userId)
	if err != nil {
		return nil, err
	}

	annos, err := c.doRequest(ctx, http.MethodDelete, urlRemoveUserFromSite, nil, nil)
	if err != nil {
		return nil, err
	}

	return annos, nil
}

// =============================================================================
// Groups API
// =============================================================================

// GetGroups returns a page of groups on the site.
func (c *Client) GetGroups(ctx context.Context, pageToken string) ([]Group, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetGroups, err := c.buildSiteURL(pathGroups)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetGroups, withPagination(page))

	var res groupsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetGroups, &res, nil)
	if err != nil {
		return nil, "", annos, err
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Groups.Group, nextToken, annos, nil
}

// GetGroupUsers returns a page of users in a group.
func (c *Client) GetGroupUsers(ctx context.Context, groupId, pageToken string) ([]User, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetGroupUsers, err := c.buildSiteURL(pathGroups, groupId, pathUsers)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetGroupUsers, withPagination(page))

	var res usersResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetGroupUsers, &res, nil)
	if err != nil {
		return nil, "", annos, err
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Users.User, nextToken, annos, nil
}

// AddUserToGroup adds a user to a group.
func (c *Client) AddUserToGroup(ctx context.Context, groupId, userId string) (annotations.Annotations, error) {
	urlAddUserToGroup, err := c.buildSiteURL(pathGroups, groupId, pathUsers)
	if err != nil {
		return nil, err
	}

	var res userResponse
	userBody := map[string]any{pathUser: map[string]any{"id": userId}}
	annos, err := c.doRequest(ctx, http.MethodPost, urlAddUserToGroup, &res, userBody)
	if err != nil {
		return nil, err
	}

	return annos, nil
}

// RemoveUserFromGroup removes a user from a group.
func (c *Client) RemoveUserFromGroup(ctx context.Context, groupId, userId string) (annotations.Annotations, error) {
	urlRemoveUserFromGroup, err := c.buildSiteURL(pathGroups, groupId, pathUsers, userId)
	if err != nil {
		return nil, err
	}

	annos, err := c.doRequest(ctx, http.MethodDelete, urlRemoveUserFromGroup, nil, nil)
	if err != nil {
		return nil, err
	}

	return annos, nil
}

// =============================================================================
// Projects API
// =============================================================================

// GetProjects returns a page of projects on the site.
func (c *Client) GetProjects(ctx context.Context, pageToken string) ([]Project, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetProjects, err := c.buildSiteURL(pathProjects)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetProjects, withPagination(page))

	var res projectsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetProjects, &res, nil)
	if err != nil {
		return nil, "", annos, fmt.Errorf("failed to get projects: %w", err)
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Projects.Project, nextToken, annos, nil
}

// GetProjectPermissions returns permissions for a project.
func (c *Client) GetProjectPermissions(ctx context.Context, projectID string) ([]GranteeCapabilities, annotations.Annotations, error) {
	urlGetProjectPermissions, err := c.buildSiteURL(pathProjects, projectID, pathPermissions)
	if err != nil {
		return nil, nil, err
	}

	var res permissionsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetProjectPermissions, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get project permissions: %w", err)
	}

	return res.Permissions.GranteeCapabilities, annos, nil
}

// AddProjectPermission adds a user permission to a project.
func (c *Client) AddProjectPermission(ctx context.Context, projectID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathProjects, projectID, pathPermissions}, pathUser, userID, capabilityName, capabilityMode)
}

// DeleteProjectPermission removes a user permission from a project.
func (c *Client) DeleteProjectPermission(ctx context.Context, projectID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathProjects, projectID, pathUsers, userID, capabilityName, capabilityMode)
}

// AddProjectGroupPermission adds a group permission to a project.
func (c *Client) AddProjectGroupPermission(ctx context.Context, projectID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathProjects, projectID, pathPermissions}, pathGroup, groupID, capabilityName, capabilityMode)
}

// DeleteProjectGroupPermission removes a group permission from a project.
func (c *Client) DeleteProjectGroupPermission(ctx context.Context, projectID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathProjects, projectID, pathGroups, groupID, capabilityName, capabilityMode)
}

// =============================================================================
// Workbooks API
// =============================================================================

// GetWorkbooks returns a page of workbooks on the site, optionally filtered by project name.
func (c *Client) GetWorkbooks(ctx context.Context, pageToken string, opts ...ReqOpt) ([]Workbook, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetWorkbooks, err := c.buildSiteURL(pathWorkbooks)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetWorkbooks, append([]ReqOpt{withPagination(page)}, opts...)...)

	var res workbooksResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetWorkbooks, &res, nil)
	if err != nil {
		return nil, "", annos, fmt.Errorf("failed to get workbooks: %w", err)
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Workbooks.Workbook, nextToken, annos, nil
}

// GetWorkbook returns a single workbook by ID.
func (c *Client) GetWorkbook(ctx context.Context, workbookID string) (*Workbook, annotations.Annotations, error) {
	urlGetWorkbook, err := c.buildSiteURL(pathWorkbooks, workbookID)
	if err != nil {
		return nil, nil, err
	}

	var res workbookResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetWorkbook, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get workbook: %w", err)
	}

	return &res.Workbook, annos, nil
}

// GetWorkbookViews returns views for a specific workbook.
func (c *Client) GetWorkbookViews(ctx context.Context, workbookID string) ([]View, annotations.Annotations, error) {
	urlGetWorkbookViews, err := c.buildSiteURL(pathWorkbooks, workbookID, pathViews)
	if err != nil {
		return nil, nil, err
	}

	var res viewsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetWorkbookViews, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get workbook views: %w", err)
	}

	return res.Views.View, annos, nil
}

// GetWorkbookPermissions returns permissions for a workbook.
func (c *Client) GetWorkbookPermissions(ctx context.Context, workbookID string) ([]GranteeCapabilities, annotations.Annotations, error) {
	urlGetWorkbookPermissions, err := c.buildSiteURL(pathWorkbooks, workbookID, pathPermissions)
	if err != nil {
		return nil, nil, err
	}

	var res permissionsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetWorkbookPermissions, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get workbook permissions: %w", err)
	}

	return res.Permissions.GranteeCapabilities, annos, nil
}

// AddWorkbookPermission adds a user permission to a workbook.
func (c *Client) AddWorkbookPermission(ctx context.Context, workbookID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathWorkbooks, workbookID, pathPermissions}, pathUser, userID, capabilityName, capabilityMode)
}

// DeleteWorkbookPermission removes a user permission from a workbook.
func (c *Client) DeleteWorkbookPermission(ctx context.Context, workbookID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathWorkbooks, workbookID, pathUsers, userID, capabilityName, capabilityMode)
}

// AddWorkbookGroupPermission adds a group permission to a workbook.
func (c *Client) AddWorkbookGroupPermission(ctx context.Context, workbookID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathWorkbooks, workbookID, pathPermissions}, "group", groupID, capabilityName, capabilityMode)
}

// DeleteWorkbookGroupPermission removes a group permission from a workbook.
func (c *Client) DeleteWorkbookGroupPermission(ctx context.Context, workbookID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathWorkbooks, workbookID, pathGroups, groupID, capabilityName, capabilityMode)
}

// =============================================================================
// Views API
// =============================================================================

// GetViewPermissions returns permissions for a view.
func (c *Client) GetViewPermissions(ctx context.Context, viewID string) ([]GranteeCapabilities, annotations.Annotations, error) {
	urlGetViewPermissions, err := c.buildSiteURL(pathViews, viewID, pathPermissions)
	if err != nil {
		return nil, nil, err
	}

	var res permissionsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetViewPermissions, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get view permissions: %w", err)
	}

	return res.Permissions.GranteeCapabilities, annos, nil
}

// AddViewPermission adds a user permission to a view.
func (c *Client) AddViewPermission(ctx context.Context, viewID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathViews, viewID, pathPermissions}, pathUser, userID, capabilityName, capabilityMode)
}

// DeleteViewPermission removes a user permission from a view.
func (c *Client) DeleteViewPermission(ctx context.Context, viewID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathViews, viewID, pathUsers, userID, capabilityName, capabilityMode)
}

// AddViewGroupPermission adds a group permission to a view.
func (c *Client) AddViewGroupPermission(ctx context.Context, viewID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathViews, viewID, pathPermissions}, pathGroup, groupID, capabilityName, capabilityMode)
}

// DeleteViewGroupPermission removes a group permission from a view.
func (c *Client) DeleteViewGroupPermission(ctx context.Context, viewID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathViews, viewID, pathGroups, groupID, capabilityName, capabilityMode)
}

// =============================================================================
// IDP Configurations API
// =============================================================================

// ListIdpConfigurations returns IDP configurations for the site.
func (c *Client) ListIdpConfigurations(ctx context.Context) ([]IdpConfiguration, annotations.Annotations, error) {
	urlListIdpConfigurations, err := c.buildSiteURL(pathIdpConfigurations)
	if err != nil {
		return nil, nil, err
	}

	var res idpConfigurationsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlListIdpConfigurations, &res, nil)
	if err != nil {
		return nil, nil, err
	}

	return res.SiteAuthConfigurations.SiteAuthConfiguration, annos, nil
}

// ListEnabledIdpConfigurations returns only enabled SAML/OIDC IDP configurations.
// The Tableau API does not support server-side filtering, so this lists all
// configurations and filters client-side.
func (c *Client) ListEnabledIdpConfigurations(ctx context.Context) ([]IdpConfiguration, annotations.Annotations, error) {
	configs, annos, err := c.ListIdpConfigurations(ctx)
	if err != nil {
		return nil, annos, err
	}

	var enabled []IdpConfiguration
	for _, cfg := range configs {
		if cfg.Enabled && isAllowedAuthSetting(cfg.AuthSetting) {
			enabled = append(enabled, cfg)
		}
	}

	return enabled, annos, nil
}

// FindIdpConfigurationByName returns the enabled IDP configuration matching the given name
// (case-insensitive). Returns nil if no match is found.
func (c *Client) FindIdpConfigurationByName(ctx context.Context, name string) (*IdpConfiguration, annotations.Annotations, error) {
	configs, annos, err := c.ListEnabledIdpConfigurations(ctx)
	if err != nil {
		return nil, annos, err
	}

	lowerName := strings.ToLower(name)
	for i := range configs {
		if strings.ToLower(configs[i].IdpConfigurationName) == lowerName {
			return &configs[i], annos, nil
		}
	}

	return nil, annos, nil
}

// =============================================================================
// HTTP Helpers
// =============================================================================

// addPermission is a shared helper for PUT permission requests (user or group).
func (c *Client) addPermission(ctx context.Context, pathSegments []string, granteeType, granteeID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	urlAddPermission, err := c.buildSiteURL(pathSegments...)
	if err != nil {
		return nil, err
	}

	body := permissionBody(granteeType, granteeID, capabilityName, capabilityMode)

	annos, err := c.doRequest(ctx, http.MethodPut, urlAddPermission, nil, body)
	if err != nil {
		return nil, fmt.Errorf("failed to add %s permission: %w", granteeType, err)
	}

	return annos, nil
}

// deletePermission is a shared helper for DELETE permission requests.
func (c *Client) deletePermission(ctx context.Context, resourcePath, resourceID, granteePath, granteeID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	urlDeletePermission, err := c.buildSiteURL(resourcePath, resourceID, pathPermissions, granteePath, granteeID, capabilityName, capabilityMode)
	if err != nil {
		return nil, err
	}

	annos, err := c.doRequest(ctx, http.MethodDelete, urlDeletePermission, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete permission: %w", err)
	}

	return annos, nil
}

// permissionBody builds the request body for adding a permission (user or group).
func permissionBody(granteeType, granteeID, capabilityName, capabilityMode string) map[string]any {
	return map[string]any{
		"permissions": map[string]any{
			"granteeCapabilities": []map[string]any{
				{
					granteeType: map[string]any{
						"id": granteeID,
					},
					"capabilities": map[string]any{
						"capability": []map[string]any{
							{
								"name": capabilityName,
								"mode": capabilityMode,
							},
						},
					},
				},
			},
		},
	}
}

// doRequest executes an HTTP request against the Tableau API.
func (c *Client) doRequest(ctx context.Context, method string, url *url.URL, res, body any) (annotations.Annotations, error) {
	reqOpts := []uhttp.RequestOption{
		uhttp.WithAcceptJSONHeader(),
		uhttp.WithContentTypeJSONHeader(),
		uhttp.WithHeader("X-Tableau-Auth", c.authToken),
	}
	if body != nil {
		reqOpts = append(reqOpts, uhttp.WithJSONBody(body))
	}

	req, err := c.httpClient.NewRequest(ctx, method, url, reqOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	var rlData v2.RateLimitDescription
	doOpts := []uhttp.DoOption{
		uhttp.WithRatelimitData(&rlData),
	}
	if res != nil {
		doOpts = append(doOpts, uhttp.WithJSONResponse(res))
	}

	resp, err := c.httpClient.Do(req, doOpts...)
	annos := annotations.New(&rlData)
	if err != nil {
		detail := extractTableauError(resp)
		if detail != "" {
			return annos, fmt.Errorf("%s request failed (%s): %w", method, detail, err)
		}
		return annos, fmt.Errorf("%s request failed: %w", method, err)
	}
	defer resp.Body.Close()

	return annos, nil
}
