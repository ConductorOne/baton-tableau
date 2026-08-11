package config

import (
	"testing"

	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/stretchr/testify/require"
)

// base returns a configuration with everything but credentials filled in, so
// each case below varies only the thing under test.
func base() *Tableau {
	return &Tableau{
		ServerPath: "https://prod-uk-a.online.tableau.com",
		SiteId:     "example",
		ApiVersion: "3.27",
	}
}

// TestConfigConstraints pins the credential rules. The personal access token
// case is the backward-compatibility guard: existing deployments supply only
// those two fields and must keep validating after connected apps were added.
func TestConfigConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Tableau)
		wantErr string
	}{
		{
			name: "personal access token alone is accepted",
			mutate: func(c *Tableau) {
				c.AccessTokenName = "conductor1"
				c.AccessTokenSecret = "secret"
			},
		},
		{
			name: "connected app alone is accepted",
			mutate: func(c *Tableau) {
				c.ConnectedAppClientId = "client-id"
				c.ConnectedAppSecretId = "secret-id"
				c.ConnectedAppSecretValue = "secret-value"
				c.ConnectedAppUsername = "svc.tableau@example.com"
			},
		},
		{
			name:    "no credentials at all is rejected",
			mutate:  func(c *Tableau) {},
			wantErr: "access-token-name",
		},
		{
			name: "half a personal access token is rejected",
			mutate: func(c *Tableau) {
				c.AccessTokenName = "conductor1"
			},
			wantErr: "access-token-secret",
		},
		{
			name: "half a connected app is rejected",
			mutate: func(c *Tableau) {
				c.ConnectedAppClientId = "client-id"
				c.ConnectedAppSecretId = "secret-id"
			},
			wantErr: "connected-app-secret-value",
		},
		{
			// The exclusivity rule names only one field per credential set, so
			// these cases lean on the required-together rules to reject a
			// partial set alongside a complete one.
			name: "a stray token secret beside a connected app is rejected",
			mutate: func(c *Tableau) {
				c.AccessTokenSecret = "secret"
				c.ConnectedAppClientId = "client-id"
				c.ConnectedAppSecretId = "secret-id"
				c.ConnectedAppSecretValue = "secret-value"
				c.ConnectedAppUsername = "svc.tableau@example.com"
			},
			wantErr: "access-token-name",
		},
		{
			name: "a stray connected app field beside a token is rejected",
			mutate: func(c *Tableau) {
				c.AccessTokenName = "conductor1"
				c.AccessTokenSecret = "secret"
				c.ConnectedAppSecretValue = "secret-value"
			},
			wantErr: "connected-app-client-id",
		},
		{
			name: "both credential sets together are rejected",
			mutate: func(c *Tableau) {
				c.AccessTokenName = "conductor1"
				c.AccessTokenSecret = "secret"
				c.ConnectedAppClientId = "client-id"
				c.ConnectedAppSecretId = "secret-id"
				c.ConnectedAppSecretValue = "secret-value"
				c.ConnectedAppUsername = "svc.tableau@example.com"
			},
			wantErr: "mutually exclusive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := base()
			test.mutate(cfg)

			err := field.Validate(Config, cfg)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

// TestConfigConstraintsAreWellFormed catches a silently disabled rule. The SDK
// returns an Invalid relationship rather than an error when a constraint is
// built wrongly — for instance if a field named in a mutually exclusive rule
// is also marked required — and an Invalid rule is never enforced.
func TestConfigConstraintsAreWellFormed(t *testing.T) {
	t.Parallel()

	for i, constraint := range ConfigurationConstraints {
		require.NotEqual(t, field.Invalid, constraint.Kind, "constraint %d is invalid and would not be enforced", i)
	}
}
