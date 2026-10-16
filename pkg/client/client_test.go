package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/stretchr/testify/require"
)

const testSiteId = "site-1"

// newTestClient builds a Client wired directly to srv, bypassing New's
// Login/auth-signin flow so tests only exercise the endpoint under test.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()

	base, err := uhttp.NewBaseHttpClientWithContext(context.Background(), srv.Client())
	require.NoError(t, err)

	return &Client{
		httpClient: base,
		baseUrl:    srv.URL,
		authToken:  "test-token",
		siteId:     testSiteId,
	}
}

func TestGetUser_Found(t *testing.T) {
	t.Parallel()

	wantPath := "/sites/" + testSiteId + "/users/user-1"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(userResponse{
			User: User{ID: "user-1", Email: "user1@example.com", SiteRole: "SiteAdministratorCreator"},
		})
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)

	user, _, err := c.GetUser(context.Background(), "user-1")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, "user-1", user.ID)
	require.Equal(t, "SiteAdministratorCreator", user.SiteRole)
}

func TestGetUser_NotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"summary":"Not Found","detail":"User not found","code":"0x1"}}`))
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, srv)

	user, _, err := c.GetUser(context.Background(), "missing-user")
	require.Error(t, err)
	require.Nil(t, user)
	require.True(t, IsNotFoundError(err), "expected IsNotFoundError(err) to be true, got err: %v", err)
}
