package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const signinResponse = `{"credentials":{"token":"session-token","site":{"id":"site-id","contentUrl":"thg_plc"}}}`

// signinRecorder stands in for Tableau's /auth/signin endpoint and captures the
// credentials object the client sent.
func signinRecorder(t *testing.T, captured *map[string]any) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.True(t, strings.HasSuffix(r.URL.Path, "/auth/signin"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var payload struct {
			Credentials map[string]any `json:"credentials"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))
		*captured = payload.Credentials

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(signinResponse))
	}))
}

func TestLogin_PersonalAccessToken(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := signinRecorder(t, &captured)
	defer server.Close()

	cfg := Config{
		SiteID:              "thg_plc",
		BaseURLOverride:     server.URL,
		PersonalAccessToken: &PersonalAccessToken{Name: "conductor1", Secret: "token-secret"},
	}

	credentials, err := Login(context.Background(), server.Client(), server.URL, cfg)
	require.NoError(t, err)
	require.Equal(t, "session-token", credentials.Token)

	require.Equal(t, "conductor1", captured["personalAccessTokenName"])
	require.Equal(t, "token-secret", captured["personalAccessTokenSecret"])
	require.NotContains(t, captured, "jwt")

	site, ok := captured["site"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "thg_plc", site["contentUrl"])
}

func TestLogin_ConnectedApp(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := signinRecorder(t, &captured)
	defer server.Close()

	cfg := Config{
		SiteID:          "thg_plc",
		BaseURLOverride: server.URL,
		ConnectedApp:    &testApp,
	}

	credentials, err := Login(context.Background(), server.Client(), server.URL, cfg)
	require.NoError(t, err)
	require.Equal(t, "session-token", credentials.Token)

	assertion, ok := captured["jwt"].(string)
	require.True(t, ok, "connected app sign-in sends a jwt attribute")
	require.Len(t, strings.Split(assertion, "."), 3)
	require.NotContains(t, captured, "personalAccessTokenName")

	site, ok := captured["site"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "thg_plc", site["contentUrl"])
}

// TestLoginCredentials_Ambiguous covers the two configurations the schema
// constraints are meant to prevent. Failing here keeps an ambiguous or empty
// credential from reaching Tableau as a confusing 401.
func TestLoginCredentials_Ambiguous(t *testing.T) {
	t.Parallel()

	_, err := loginCredentials(Config{})
	require.ErrorContains(t, err, "no credentials supplied")

	_, err = loginCredentials(Config{
		PersonalAccessToken: &PersonalAccessToken{Name: "n", Secret: "s"},
		ConnectedApp:        &testApp,
	})
	require.ErrorContains(t, err, "use one or the other")
}

// TestLogin_SurfacesTableauError asserts the upstream message survives. A
// generic failure here is what turns a dead credential into a multi-day
// outage, because the operator never learns the token was rejected.
func TestLogin_SurfacesTableauError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"401001","summary":"Signin Error","detail":"The personal access token you provided is invalid."}}`))
	}))
	defer server.Close()

	cfg := Config{
		SiteID:              "thg_plc",
		BaseURLOverride:     server.URL,
		PersonalAccessToken: &PersonalAccessToken{Name: "stale", Secret: "fresh"},
	}

	_, err := Login(context.Background(), server.Client(), server.URL, cfg)
	require.ErrorContains(t, err, "401")
	require.ErrorContains(t, err, "personal access token you provided is invalid")
}
