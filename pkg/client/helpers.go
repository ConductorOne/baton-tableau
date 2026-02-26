package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	DefaultAPIVersion = "3.27"

	defaultPageSize = 100

	authSignin              = "auth/signin"
	pathSites               = "sites"
	pathUser                = "user"
	pathGroup               = "group"
	pathUsers               = "users"
	pathGroups              = "groups"
	pathProjects            = "projects"
	pathWorkbooks           = "workbooks"
	pathViews               = "views"
	pathPermissions         = "permissions"
	pathDefaultPermissions  = "default-permissions"
	pathIdpConfigurations   = "site-auth-configurations"
)

// BuildBaseURL constructs the Tableau REST API base URL from a server path and API version.
// It preserves the scheme if provided (useful for HTTP-based testing), otherwise defaults
// to HTTPS. The apiVersion parameter is expected to be non-empty; the caller (config layer)
// is responsible for providing a default via field.WithDefaultValue.
func BuildBaseURL(serverPath, apiVersion string) (string, error) {
	if !strings.Contains(serverPath, "://") {
		serverPath = fmt.Sprintf("https://%s", serverPath)
	}
	base, err := url.Parse(serverPath)
	if err != nil {
		return "", fmt.Errorf("invalid server path %q: %w", serverPath, err)
	}

	return url.JoinPath(base.String(), "api", apiVersion)
}

// parsePageToken parses a page token string to a 1-based page number, defaulting to 1.
func parsePageToken(token string) int {
	if token == "" {
		return 1
	}
	page, err := strconv.Atoi(token)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// getNextPageToken calculates the next page token based on current page and API pagination.
// Returns "" when there are no more pages.
func getNextPageToken(currentPage int, pag Pagination) string {
	pageSize, err := strconv.Atoi(pag.PageSize)
	if err != nil || pageSize <= 0 {
		return ""
	}
	totalAvailable, err := strconv.Atoi(pag.TotalAvailable)
	if err != nil {
		return ""
	}
	if currentPage*pageSize >= totalAvailable {
		return ""
	}
	return strconv.Itoa(currentPage + 1)
}

// ReqOpt is a function that modifies a URL with query parameters.
type ReqOpt func(u *url.URL)

// withIntParam adds an integer query parameter to the URL.
func withIntParam(key string, value int) ReqOpt {
	return func(u *url.URL) {
		q := u.Query()
		q.Set(key, strconv.Itoa(value))
		u.RawQuery = q.Encode()
	}
}

// withPagination adds pageSize and pageNumber query parameters.
func withPagination(page int) ReqOpt {
	return func(u *url.URL) {
		withIntParam("pageSize", defaultPageSize)(u)
		withIntParam("pageNumber", page)(u)
	}
}

// WithFilter adds a filter query parameter (e.g. "siteRole:eq:Viewer").
func WithFilter(filter string) ReqOpt {
	return func(u *url.URL) {
		q := u.Query()
		q.Set("filter", filter)
		u.RawQuery = q.Encode()
	}
}

// applyOpts applies all ReqOpts to a URL.
func applyOpts(u *url.URL, opts ...ReqOpt) {
	for _, opt := range opts {
		opt(u)
	}
}

// allowedAuthSettings are the IDP authentication types supported for user provisioning.
var allowedAuthSettings = []string{"SAML", "OPENID"}

// isAllowedAuthSetting returns true if the auth setting is an allowed IDP type.
func isAllowedAuthSetting(authSetting string) bool {
	upper := strings.ToUpper(authSetting)
	for _, allowed := range allowedAuthSettings {
		if upper == allowed {
			return true
		}
	}
	return false
}

// buildURL joins a base URL with path segments and returns a parsed *url.URL.
func buildURL(baseUrl string, segments ...string) (*url.URL, error) {
	joined, err := url.JoinPath(baseUrl, segments...)
	if err != nil {
		return nil, fmt.Errorf("failed to build URL: %w", err)
	}
	parsed, err := url.Parse(joined)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}
	return parsed, nil
}

// buildSiteURL constructs a URL path under the current site.
func (c *Client) buildSiteURL(segments ...string) (*url.URL, error) {
	parts := append([]string{pathSites, c.siteId}, segments...)
	return buildURL(c.baseUrl, parts...)
}

// extractTableauError reads the Tableau API error detail from the response body.
// Returns a human-readable string or empty if the body cannot be parsed.
func extractTableauError(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || len(bodyBytes) == 0 {
		return ""
	}

	var tableauErr tableauErrorResponse
	if json.Unmarshal(bodyBytes, &tableauErr) != nil || tableauErr.Error == nil {
		return ""
	}

	return tableauErr.Error.String()
}
