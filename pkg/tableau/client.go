package tableau

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

const (
	// 1-based not zero based.
	defaultPageNumber = 1
	defaultPageSize   = 100
)

type Client struct {
	httpClient    *uhttp.BaseHttpClient
	authToken     string
	siteId        string
	baseUrl       string
	currentUserId string
}

func NewClient(accessToken string, siteId string, baseUrl string, userId string, httpClient *uhttp.BaseHttpClient) *Client {
	return &Client{
		httpClient:    httpClient,
		authToken:     accessToken,
		siteId:        siteId,
		baseUrl:       baseUrl,
		currentUserId: userId,
	}
}

type Pagination struct {
	PageNumber     string `json:"pageNumber"`
	PageSize       string `json:"pageSize"`
	TotalAvailable string `json:"totalAvailable"`
}

type usersResponse struct {
	Pagination Pagination `json:"pagination"`
	Users      struct {
		User []User `json:"user"`
	} `json:"users"`
}

// returns query params with pagination options.
func paginationQuery(pageSize int, pageNumber int) url.Values {
	pageSizeString := strconv.Itoa(pageSize)
	pageNumberString := strconv.Itoa(pageNumber)
	q := url.Values{}
	q.Add("pageSize", pageSizeString)
	q.Add("pageNumber", pageNumberString)
	return q
}

// Login returns credentials needed to use the API.
func Login(ctx context.Context, baseUrl string, contentUrl string, token string, tokenName string) (Credentials, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return Credentials{}, err
	}

	input, err := json.Marshal(map[string]interface{}{
		"credentials": map[string]interface{}{
			"personalAccessTokenName":   tokenName,
			"personalAccessTokenSecret": token,
			"site": map[string]string{
				"contentUrl": contentUrl,
			},
		},
	})
	if err != nil {
		return Credentials{}, err
	}

	url := fmt.Sprint(baseUrl, "/auth/signin")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(input))
	if err != nil {
		return Credentials{}, err
	}

	req.Header.Add("content-type", "application/json")
	req.Header.Add("accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return Credentials{}, err
	}

	defer resp.Body.Close()

	var res struct {
		Credentials Credentials `json:"credentials"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return Credentials{}, err
	}
	return res.Credentials, nil
}

// GetSite returns site details of the site user is logged in to.
func (c *Client) GetSite(ctx context.Context) (Site, error) {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId)

	var res struct {
		Site Site `json:"site"`
	}
	if err := c.doRequest(ctx, url, &res, nil, nil, http.MethodGet); err != nil {
		return Site{}, err
	}

	return res.Site, nil
}

// GetUsers returns all users on site.
func (c *Client) GetUsers(ctx context.Context, pageSize int, pageNumber int) ([]User, Pagination, error) {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/users")
	q := paginationQuery(pageSize, pageNumber)

	var res usersResponse
	if err := c.doRequest(ctx, url, &res, q, nil, http.MethodGet); err != nil {
		return nil, Pagination{}, err
	}

	return res.Users.User, res.Pagination, nil
}

// GetGroups returns all groups on site.
func (c *Client) GetGroups(ctx context.Context, pageSize int, pageNumber int) ([]Group, Pagination, error) {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/groups")
	q := paginationQuery(pageSize, pageNumber)

	var res struct {
		Pagination Pagination `json:"pagination"`
		Groups     struct {
			Group []Group `json:"group"`
		} `json:"groups"`
	}

	if err := c.doRequest(ctx, url, &res, q, nil, http.MethodGet); err != nil {
		return nil, Pagination{}, err
	}

	return res.Groups.Group, res.Pagination, nil
}

// GetGroupUsers returns all users in a group.
func (c *Client) GetGroupUsers(ctx context.Context, groupId string, pageSize int, pageNumber int) ([]User, Pagination, error) {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/groups/", groupId, "/users")
	q := paginationQuery(pageSize, pageNumber)

	var res usersResponse
	if err := c.doRequest(ctx, url, &res, q, nil, http.MethodGet); err != nil {
		return nil, Pagination{}, err
	}

	return res.Users.User, res.Pagination, nil
}

type usersToken struct {
	Page int64 `json:"page"`
}

func (c *Client) GetUsersPage(ctx context.Context, token string) ([]User, string, error) {
	var users []User
	pageNumber := defaultPageNumber
	uToken := &usersToken{}
	if err := json.Unmarshal([]byte(token), &uToken); err == nil {
		pageNumber = int(uToken.Page)
	}
	totalReturned := 0

	allUsers, paginationData, err := c.GetUsers(ctx, defaultPageSize, pageNumber)
	if err != nil {
		return nil, "", fmt.Errorf("tableau-connector: failed to list users: %w", err)
	}

	pageSizeInt, err := strconv.Atoi(paginationData.PageSize)
	if err != nil {
		return nil, "", err
	}

	totalReturned += pageSizeInt
	totalAvailableInt, err := strconv.Atoi(paginationData.TotalAvailable)
	if err != nil {
		return nil, "", err
	}

	users = append(users, allUsers...)

	if totalReturned >= totalAvailableInt {
		return users, "", nil
	}

	uToken.Page = int64(pageNumber + 1)
	newToken, err := json.Marshal(uToken)
	if err != nil {
		return nil, "", err
	}

	return users, string(newToken), nil
}

// GetPaginatedUsers returns all users - paginated.
func (c *Client) GetPaginatedUsers(ctx context.Context) ([]User, error) {
	var users []User
	pageNumber := defaultPageNumber
	totalReturned := 0

	for {
		allUsers, paginationData, err := c.GetUsers(ctx, defaultPageSize, pageNumber)
		if err != nil {
			return nil, fmt.Errorf("tableau-connector: failed to list users: %w", err)
		}

		totalReturned += len(allUsers)
		totalAvailableInt, err := strconv.Atoi(paginationData.TotalAvailable)
		if err != nil {
			return nil, err
		}

		users = append(users, allUsers...)

		if totalReturned >= totalAvailableInt {
			break
		}
		pageNumber += 1
	}

	return users, nil
}

// GetPaginatedGroups returns all groups - paginated.
func (c *Client) GetPaginatedGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	pageNumber := defaultPageNumber
	totalReturned := 0

	for {
		allGroups, paginationData, err := c.GetGroups(ctx, defaultPageSize, pageNumber)
		if err != nil {
			return nil, fmt.Errorf("tableau-connector: failed to list groups: %w", err)
		}

		totalReturned += len(allGroups)
		totalAvailableInt, err := strconv.Atoi(paginationData.TotalAvailable)
		if err != nil {
			return nil, err
		}

		groups = append(groups, allGroups...)

		if totalReturned >= totalAvailableInt {
			break
		}
		pageNumber += 1
	}

	return groups, nil
}

// GetPaginatedGroupUsers returns all users in a group - paginated.
func (c *Client) GetPaginatedGroupUsers(ctx context.Context, groupId string) ([]User, error) {
	var users []User
	pageNumber := defaultPageNumber
	totalReturned := 0

	for {
		allUsers, paginationData, err := c.GetGroupUsers(ctx, groupId, defaultPageSize, pageNumber)
		if err != nil {
			return nil, fmt.Errorf("tableau-connector: failed to list group users: %w", err)
		}

		totalReturned += len(allUsers)
		totalAvailableInt, err := strconv.Atoi(paginationData.TotalAvailable)
		if err != nil {
			return nil, err
		}

		users = append(users, allUsers...)

		if totalReturned >= totalAvailableInt {
			break
		}
		pageNumber += 1
	}

	return users, nil
}

// VerifyUser returns current logged in user.
func (c *Client) VerifyUser(ctx context.Context) error {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/users/", c.currentUserId)

	var res struct {
		User User `json:"user"`
	}

	if err := c.doRequest(ctx, url, &res, nil, nil, http.MethodGet); err != nil {
		return err
	}

	return nil
}

// AddUserToGroup adds user to a group.
func (c *Client) AddUserToGroup(ctx context.Context, groupId, userId string) error {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/groups/", groupId, "/users")
	var res struct {
		User User `json:"user"`
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"user": map[string]interface{}{
			"id": userId,
		},
	})

	if err != nil {
		return err
	}

	if err := c.doRequest(ctx, url, &res, nil, requestBody, http.MethodPost); err != nil {
		return err
	}

	return nil
}

// RemoveUserFromGroup removes user from a group.
func (c *Client) RemoveUserFromGroup(ctx context.Context, groupId, userId string) error {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/groups/", groupId, "/users/", userId)

	if err := c.doRequest(ctx, url, nil, nil, nil, http.MethodDelete); err != nil {
		return err
	}

	return nil
}

func (c *Client) doRequest(ctx context.Context, url string, res interface{}, q url.Values, body []byte, method string) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tableau-connector: failed to create request: %w", err)
	}

	if q != nil {
		req.URL.RawQuery = q.Encode()
	}

	req.Header.Add("X-Tableau-Auth", fmt.Sprint(c.authToken))
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")

	// For DELETE requests, don't expect a JSON response
	if method == http.MethodDelete {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("tableau-connector: %s request failed: %w", method, err)
		}
		defer resp.Body.Close()
		return nil
	}

	// For other methods, parse JSON response
	resp, err := c.httpClient.Do(req, uhttp.WithJSONResponse(res))
	if err != nil {
		return fmt.Errorf("tableau-connector: %s request failed: %w", method, err)
	}
	defer resp.Body.Close()
	return nil
}


func (c *Client) AddUserToSite(ctx context.Context, user CreateUserRequest) (*User, error) {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/users")
	var res struct {
		User *User `json:"user"`
	}

	userMap := map[string]interface{}{
		"name":     user.Email,
		"siteRole": user.SiteRole,
	}

	if user.IdpConfigurationId != "" {
		userMap["idpConfigurationId"] = user.IdpConfigurationId
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"user": userMap,
	})

	if err != nil {
		return nil, err
	}

	if err := c.doRequest(ctx, url, &res, nil, requestBody, http.MethodPost); err != nil {
		return nil, err
	}

	if res.User == nil {
		return nil, fmt.Errorf("failed to create user, no user returned")
	}

	return res.User, nil
}

func (c *Client) RemoveUserFromSite(ctx context.Context, userId string) error {
	url := fmt.Sprint(c.baseUrl, "/sites/", c.siteId, "/users/", userId)
	if err := c.doRequest(ctx, url, nil, nil, nil, http.MethodDelete); err != nil {
		return err
	}

	return nil
}

func (c *Client) UpdateUserSiteRole(ctx context.Context, userId string, siteRole string) error {
	url, err := buildResourceURL(c.baseUrl, "/sites/"+c.siteId+"/users", userId)
	if err != nil {
		return err
	}
	var res struct {
		User User `json:"user"`
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"user": map[string]interface{}{
			"siteRole": siteRole,
		},
	})

	if err != nil {
		return err
	}

	if err := c.doRequest(ctx, url, &res, nil, requestBody, http.MethodPut); err != nil {
		return err
	}

	return nil
}

func (c *Client) GetViews(ctx context.Context, pageSize int, pageNumber int) ([]View, Pagination, error) {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views")
	if err != nil {
		return nil, Pagination{}, fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}
	q := paginationQuery(pageSize, pageNumber)

	var res struct {
		Pagination Pagination `json:"pagination"`
		Views      struct {
			View []View `json:"view"`
		} `json:"views"`
	}

	if err := c.doRequest(ctx, endpoint, &res, q, nil, http.MethodGet); err != nil {
		return nil, Pagination{}, fmt.Errorf("tableau-connector: failed to get views: %w", err)
	}

	return res.Views.View, res.Pagination, nil
}

func (c *Client) GetPaginatedViews(ctx context.Context) ([]View, error) {
	var views []View
	pageNumber := defaultPageNumber
	totalReturned := 0

	for {
		allViews, paginationData, err := c.GetViews(ctx, defaultPageSize, pageNumber)
		if err != nil {
			return nil, fmt.Errorf("tableau-connector: failed to list views: %w", err)
		}

		totalReturned += len(allViews)
		totalAvailableInt, err := strconv.Atoi(paginationData.TotalAvailable)
		if err != nil {
			return nil, fmt.Errorf("tableau-connector: failed to parse total available: %w", err)
		}

		views = append(views, allViews...)

		if totalReturned >= totalAvailableInt {
			break
		}
		pageNumber += 1
	}

	return views, nil
}

func (c *Client) GetViewPermissions(ctx context.Context, viewID string) ([]GranteeCapabilities, error) {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views", viewID, "permissions")
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}

	var res struct {
		Permissions struct {
			GranteeCapabilities []GranteeCapabilities `json:"granteeCapabilities"`
		} `json:"permissions"`
	}

	if err := c.doRequest(ctx, endpoint, &res, nil, nil, http.MethodGet); err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to get view permissions: %w", err)
	}

	return res.Permissions.GranteeCapabilities, nil
}

func (c *Client) AddViewPermission(ctx context.Context, viewID, userID, capabilityName, capabilityMode string) error {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views", viewID, "permissions")
	if err != nil {
		return fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"permissions": map[string]interface{}{
			"granteeCapabilities": []map[string]interface{}{
				{
					"user": map[string]interface{}{
						"id": userID,
					},
					"capabilities": map[string]interface{}{
						"capability": []map[string]interface{}{
							{
								"name": capabilityName,
								"mode": capabilityMode,
							},
						},
					},
				},
			},
		},
	})

	if err != nil {
		return fmt.Errorf("tableau-connector: failed to marshal view permission request: %w", err)
	}

	var res struct{}
	if err := c.doRequest(ctx, endpoint, &res, nil, requestBody, http.MethodPut); err != nil {
		return fmt.Errorf("tableau-connector: failed to add view permission: %w", err)
	}

	return nil
}

func (c *Client) DeleteViewPermission(ctx context.Context, viewID, userID, capabilityName, capabilityMode string) error {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views", viewID, "permissions", "users", userID, capabilityName, capabilityMode)
	if err != nil {
		return fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}

	if err := c.doRequest(ctx, endpoint, nil, nil, nil, http.MethodDelete); err != nil {
		return fmt.Errorf("tableau-connector: failed to delete view permission: %w", err)
	}

	return nil
}

func (c *Client) AddViewGroupPermission(ctx context.Context, viewID, groupID, capabilityName, capabilityMode string) error {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views", viewID, "permissions")
	if err != nil {
		return fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"permissions": map[string]interface{}{
			"granteeCapabilities": []map[string]interface{}{
				{
					"group": map[string]interface{}{
						"id": groupID,
					},
					"capabilities": map[string]interface{}{
						"capability": []map[string]interface{}{
							{
								"name": capabilityName,
								"mode": capabilityMode,
							},
						},
					},
				},
			},
		},
	})

	if err != nil {
		return fmt.Errorf("tableau-connector: failed to marshal view group permission request: %w", err)
	}

	var res struct{}
	if err := c.doRequest(ctx, endpoint, &res, nil, requestBody, http.MethodPut); err != nil {
		return fmt.Errorf("tableau-connector: failed to add view group permission: %w", err)
	}

	return nil
}

func (c *Client) DeleteViewGroupPermission(ctx context.Context, viewID, groupID, capabilityName, capabilityMode string) error {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "views", viewID, "permissions", "groups", groupID, capabilityName, capabilityMode)
	if err != nil {
		return fmt.Errorf("tableau-connector: failed to build URL: %w", err)
	}

	if err := c.doRequest(ctx, endpoint, nil, nil, nil, http.MethodDelete); err != nil {
		return fmt.Errorf("tableau-connector: failed to delete view group permission: %w", err)
	}

	return nil
}

func (c *Client) ListIdpConfigurations(ctx context.Context) ([]IdpConfiguration, error) {
	endpoint, err := url.JoinPath(c.baseUrl, "sites", c.siteId, "site-auth-configurations")
	if err != nil {
		return nil, fmt.Errorf("failed to build URL: %w", err)
	}

	var res struct {
		SiteAuthConfigurations struct {
			SiteAuthConfiguration []IdpConfiguration `json:"siteAuthConfiguration"`
		} `json:"siteAuthConfigurations"`
	}

	if err := c.doRequest(ctx, endpoint, &res, nil, nil, http.MethodGet); err != nil {
		return nil, err
	}

	return res.SiteAuthConfigurations.SiteAuthConfiguration, nil
}
func buildResourceURL(baseURL string, endpoint string, elems ...string) (string, error) {
	joined, err := url.JoinPath(baseURL, append([]string{endpoint}, elems...)...)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}
	return joined, nil
}
