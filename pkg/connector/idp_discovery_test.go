package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/stretchr/testify/require"
)

// idpDiscoveryServer answers sign-in normally and refuses the IDP discovery
// endpoint with the given status. That is the shape Tableau presents to a
// connected app: the session is valid, but no published scope covers
// site-auth-configurations, so the lookup alone is turned away.
func idpDiscoveryServer(t *testing.T, discoveryStatus int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/signin"):
			_, _ = w.Write([]byte(`{"credentials":{"token":"session-token","site":{"id":"site-id","contentUrl":"thg_plc"}}}`))
		case strings.HasSuffix(r.URL.Path, "/site-auth-configurations"):
			w.WriteHeader(discoveryStatus)
			_, _ = w.Write([]byte(`{"error":{"code":"403004","summary":"Forbidden","detail":"insufficient scope"}}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

func newTestUserBuilder(t *testing.T, server *httptest.Server) *userBuilder {
	t.Helper()

	c, err := client.New(context.Background(), client.Config{
		SiteID:          "thg_plc",
		BaseURLOverride: server.URL,
		ConnectedApp: &client.ConnectedApp{
			ClientID:    "client-id",
			SecretID:    "secret-id",
			SecretValue: "secret-value",
			Username:    "svc.tableau@example.com",
		},
	})
	require.NoError(t, err)

	return newUserBuilder(c)
}

// TestSelectIDPConfiguration_DiscoveryRefused pins the behaviour that decides
// whether connected app account provisioning works at all. Without an explicit
// IDP name there is nothing to resolve, so a refused lookup must degrade to the
// site default rather than fail the whole account creation.
func TestSelectIDPConfiguration_DiscoveryRefused(t *testing.T) {
	t.Parallel()

	for _, discoveryStatus := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(discoveryStatus), func(t *testing.T) {
			t.Parallel()

			server := idpDiscoveryServer(t, discoveryStatus)
			defer server.Close()

			idpID, err := newTestUserBuilder(t, server).selectIDPConfiguration(context.Background(), "")
			require.NoError(t, err)
			require.Empty(t, idpID, "an unresolvable lookup falls back to the site default auth setting")
		})
	}
}

// TestSelectIDPConfiguration_DiscoveryRefusedWithExplicitName is the other half:
// once the caller names an IDP, silently ignoring it would put the account on
// the wrong authentication type, so the refusal has to surface.
func TestSelectIDPConfiguration_DiscoveryRefusedWithExplicitName(t *testing.T) {
	t.Parallel()

	for _, discoveryStatus := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(discoveryStatus), func(t *testing.T) {
			t.Parallel()

			server := idpDiscoveryServer(t, discoveryStatus)
			defer server.Close()

			_, err := newTestUserBuilder(t, server).selectIDPConfiguration(context.Background(), "Okta")
			require.ErrorContains(t, err, "cannot be resolved")
			require.ErrorContains(t, err, "Okta")
		})
	}
}
