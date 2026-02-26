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
//	- GET    /api/{version}/sites/{siteId}/projects                                                                              - List projects (paginated, supports filter)
//	- GET    /api/{version}/sites/{siteId}/projects?filter=id:eq:{projectId}                                                     - Get project by ID
//	- GET    /api/{version}/sites/{siteId}/projects?filter=name:eq:{projectName}                                                 - Get project by name
//	- GET    /api/{version}/sites/{siteId}/projects/{projectId}/permissions                                                      - Get project permissions
//	- PUT    /api/{version}/sites/{siteId}/projects/{projectId}/permissions                                                      - Add project permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/permissions/users/{userId}/{cap}/{mode}                          - Delete user project permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/permissions/groups/{groupId}/{cap}/{mode}                        - Delete group project permission
//	- GET    /api/{version}/sites/{siteId}/projects/{projectId}/default-permissions/workbooks                                    - Get project default workbook permissions
//	- PUT    /api/{version}/sites/{siteId}/projects/{projectId}/default-permissions/workbooks                                    - Add project default workbook permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/default-permissions/workbooks/users/{userId}/{cap}/{mode}        - Delete user default workbook permission
//	- DELETE /api/{version}/sites/{siteId}/projects/{projectId}/default-permissions/workbooks/groups/{groupId}/{cap}/{mode}      - Delete group default workbook permission
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =============================================================================
// Client
// =============================================================================

// Client is an HTTP client for the Tableau REST API.
// Authentication is deferred until Authenticate() is called explicitly.
// The Baton SDK spawns the connector in two processes (main + gRPC subprocess),
// so logging in during New() would create two Tableau sessions from the same PAT,
// causing session conflicts on Tableau Cloud.
type Client struct {
	httpClient        *uhttp.BaseHttpClient
	authToken         string
	siteId            string
	baseUrl           string
	contentUrl        string
	accessTokenName   string
	accessTokenSecret string
}

// New creates a Tableau API client without authenticating. It builds the base URL
// from serverPath and apiVersion, initializes the HTTP transport, and stores the
// PAT credentials for later use. Authentication is intentionally deferred to
// Authenticate() to avoid creating duplicate Tableau sessions — the Baton SDK
// instantiates the connector twice (main process + gRPC subprocess), but only
// the subprocess needs an active session.
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

	return &Client{
		httpClient:        baseHttpClient,
		baseUrl:           baseURL,
		contentUrl:        siteID,
		accessTokenName:   accessTokenName,
		accessTokenSecret: accessTokenSecret,
	}, nil
}

// Authenticate signs in to the Tableau REST API using the stored PAT credentials
// and populates the auth token and site ID needed for subsequent API calls.
// This is called from Connector.Validate(), which the SDK invokes once per sync
// cycle before any resource operations. If already authenticated, calling this
// again will replace the existing session with a new one.
func (c *Client) Authenticate(ctx context.Context) error {
	credentials, err := Login(ctx, c.httpClient, c.baseUrl, c.contentUrl, c.accessTokenSecret, c.accessTokenName)
	if err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}

	c.authToken = credentials.Token
	c.siteId = credentials.Site.ID

	return nil
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
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("login failed: %w", err)
	}

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

// GetUsers returns a page of users on the site, optionally filtered by query parameters.
func (c *Client) GetUsers(ctx context.Context, pageToken string, opts ...ReqOpt) ([]*User, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetUsers, err := c.buildSiteURL(pathUsers)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetUsers, append([]ReqOpt{withPagination(page)}, opts...)...)

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
func (c *Client) GetGroups(ctx context.Context, pageToken string) ([]*Group, string, annotations.Annotations, error) {
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
func (c *Client) GetGroupUsers(ctx context.Context, groupId, pageToken string) ([]*User, string, annotations.Annotations, error) {
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

// GetProjectByName returns a single project by name using a server-side filter.
// Returns nil if no project with that name is found.
func (c *Client) GetProject(ctx context.Context, name string, id string) (*Project, annotations.Annotations, error) {
	projects, _, annos, err := c.GetProjects(ctx, "", WithFilter("name:eq:"+name))
	if err != nil {
		return nil, annos, fmt.Errorf("failed to get project by name %q: %w", name, err)
	}
	if len(projects) == 0 {
		return nil, annos, status.Errorf(codes.NotFound, "Project with name %q not found", name)
	}
	for _, project := range projects {
		if project.ID == id {
			return project, annos, nil
		}
	}
	return nil, annos, status.Errorf(codes.NotFound, "Project with name %q and ID %q not found", name, id)
}

// GetProjects returns a page of projects on the site, optionally filtered by query parameters.
func (c *Client) GetProjects(ctx context.Context, pageToken string, opts ...ReqOpt) ([]*Project, string, annotations.Annotations, error) {
	page := parsePageToken(pageToken)

	urlGetProjects, err := c.buildSiteURL(pathProjects)
	if err != nil {
		return nil, "", nil, err
	}
	applyOpts(urlGetProjects, append([]ReqOpt{withPagination(page)}, opts...)...)

	var res projectsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, urlGetProjects, &res, nil)
	if err != nil {
		return nil, "", annos, fmt.Errorf("failed to get projects: %w", err)
	}

	nextToken := getNextPageToken(page, res.Pagination)
	return res.Projects.Project, nextToken, annos, nil
}

// GetProjectPermissions returns permissions for a project.
func (c *Client) GetProjectPermissions(ctx context.Context, projectID string) ([]*GranteeCapabilities, annotations.Annotations, error) {
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

// GetProjectDefaultWorkbookPermissions returns the default workbook permissions for a project.
func (c *Client) GetProjectDefaultWorkbookPermissions(ctx context.Context, projectID string) ([]*GranteeCapabilities, annotations.Annotations, error) {
	url, err := c.buildSiteURL(pathProjects, projectID, pathDefaultPermissions, pathWorkbooks)
	if err != nil {
		return nil, nil, err
	}

	var res permissionsResponse
	annos, err := c.doRequest(ctx, http.MethodGet, url, &res, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get project default workbook permissions: %w", err)
	}

	return res.Permissions.GranteeCapabilities, annos, nil
}

// AddProjectDefaultWorkbookPermission adds a default workbook user permission to a project.
func (c *Client) AddProjectDefaultWorkbookPermission(ctx context.Context, projectID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathProjects, projectID, pathDefaultPermissions, pathWorkbooks}, pathUser, userID, capabilityName, capabilityMode)
}

// DeleteProjectDefaultWorkbookPermission removes a default workbook user permission from a project.
func (c *Client) DeleteProjectDefaultWorkbookPermission(ctx context.Context, projectID, userID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	url, err := c.buildSiteURL(pathProjects, projectID, pathDefaultPermissions, pathWorkbooks, pathUsers, userID, capabilityName, capabilityMode)
	if err != nil {
		return nil, err
	}
	annos, err := c.doRequest(ctx, http.MethodDelete, url, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete project default workbook permission: %w", err)
	}
	return annos, nil
}

// AddProjectDefaultWorkbookGroupPermission adds a default workbook group permission to a project.
func (c *Client) AddProjectDefaultWorkbookGroupPermission(ctx context.Context, projectID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.addPermission(ctx, []string{pathProjects, projectID, pathDefaultPermissions, pathWorkbooks}, pathGroup, groupID, capabilityName, capabilityMode)
}

// DeleteProjectDefaultWorkbookGroupPermission removes a default workbook group permission from a project.
func (c *Client) DeleteProjectDefaultWorkbookGroupPermission(ctx context.Context, projectID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	url, err := c.buildSiteURL(pathProjects, projectID, pathDefaultPermissions, pathWorkbooks, pathGroups, groupID, capabilityName, capabilityMode)
	if err != nil {
		return nil, err
	}
	annos, err := c.doRequest(ctx, http.MethodDelete, url, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to delete project default workbook group permission: %w", err)
	}
	return annos, nil
}

// =============================================================================
// Workbooks API
// =============================================================================

// GetWorkbooks returns a page of workbooks on the site, optionally filtered by query parameters.
func (c *Client) GetWorkbooks(ctx context.Context, pageToken string, opts ...ReqOpt) ([]*Workbook, string, annotations.Annotations, error) {
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

// GetWorkbookViews returns views for a specific workbook. This endpoint does not
// support pagination — it returns all views in a single response without a
// pagination object, so no page handling is needed.
func (c *Client) GetWorkbookViews(ctx context.Context, workbookID string) ([]*View, annotations.Annotations, error) {
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
func (c *Client) GetWorkbookPermissions(ctx context.Context, workbookID string) ([]*GranteeCapabilities, annotations.Annotations, error) {
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
	return c.addPermission(ctx, []string{pathWorkbooks, workbookID, pathPermissions}, pathGroup, groupID, capabilityName, capabilityMode)
}

// DeleteWorkbookGroupPermission removes a group permission from a workbook.
func (c *Client) DeleteWorkbookGroupPermission(ctx context.Context, workbookID, groupID, capabilityName, capabilityMode string) (annotations.Annotations, error) {
	return c.deletePermission(ctx, pathWorkbooks, workbookID, pathGroups, groupID, capabilityName, capabilityMode)
}

// =============================================================================
// Views API
// =============================================================================

// GetViewPermissions returns permissions for a view.
func (c *Client) GetViewPermissions(ctx context.Context, viewID string) ([]*GranteeCapabilities, annotations.Annotations, error) {
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
func (c *Client) ListIdpConfigurations(ctx context.Context) ([]*IdpConfiguration, annotations.Annotations, error) {
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
func (c *Client) ListEnabledIdpConfigurations(ctx context.Context) ([]*IdpConfiguration, annotations.Annotations, error) {
	configs, annos, err := c.ListIdpConfigurations(ctx)
	if err != nil {
		return nil, annos, err
	}

	var enabled []*IdpConfiguration
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
	for _, cfg := range configs {
		if strings.ToLower(cfg.IdpConfigurationName) == lowerName {
			return cfg, annos, nil
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
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			if detail := extractTableauError(resp); detail != "" {
				return annos, fmt.Errorf("%s request failed (%s): %w", method, detail, err)
			}
		}
		return annos, fmt.Errorf("%s request failed: %w", method, err)
	}

	return annos, nil
}
