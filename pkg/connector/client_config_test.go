package connector

import (
	"testing"

	cfg "github.com/conductorone/baton-tableau/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestClientConfig covers the mapping from connector configuration to client
// credentials. Picking the wrong branch here is invisible until Tableau
// rejects the sign-in, so both paths are pinned.
func TestClientConfig(t *testing.T) {
	t.Parallel()

	t.Run("personal access token", func(t *testing.T) {
		t.Parallel()

		got := clientConfig(&cfg.Tableau{
			ServerPath:        "https://prod-uk-a.online.tableau.com",
			SiteId:            "example",
			ApiVersion:        "3.27",
			AccessTokenName:   "conductor1",
			AccessTokenSecret: "token-secret",
		})

		require.Nil(t, got.ConnectedApp)
		require.NotNil(t, got.PersonalAccessToken)
		require.Equal(t, "conductor1", got.PersonalAccessToken.Name)
		require.Equal(t, "token-secret", got.PersonalAccessToken.Secret)
		require.Equal(t, "example", got.SiteID)
		require.Equal(t, "3.27", got.APIVersion)
	})

	t.Run("connected app", func(t *testing.T) {
		t.Parallel()

		got := clientConfig(&cfg.Tableau{
			ServerPath:              "https://prod-uk-a.online.tableau.com",
			SiteId:                  "example",
			ApiVersion:              "3.27",
			ConnectedAppClientId:    "client-id",
			ConnectedAppSecretId:    "secret-id",
			ConnectedAppSecretValue: "secret-value",
			ConnectedAppUsername:    "svc.tableau@example.com",
		})

		require.Nil(t, got.PersonalAccessToken)
		require.NotNil(t, got.ConnectedApp)
		require.Equal(t, "client-id", got.ConnectedApp.ClientID)
		require.Equal(t, "secret-id", got.ConnectedApp.SecretID)
		require.Equal(t, "secret-value", got.ConnectedApp.SecretValue)
		require.Equal(t, "svc.tableau@example.com", got.ConnectedApp.Username)
	})
}
