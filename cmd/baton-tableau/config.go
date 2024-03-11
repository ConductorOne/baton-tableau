package main

import (
	"context"
	"fmt"

	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/spf13/cobra"
)

// config defines the external configuration required for the connector to run.
type config struct {
	cli.BaseConfig `mapstructure:",squash"` // Puts the base config options in the same place as the connector options

	AccessTokenName   string `mapstructure:"access-token-name"`
	AccessTokenSecret string `mapstructure:"access-token-secret"`
	ServerPath        string `mapstructure:"server-path"`
	SiteID            string `mapstructure:"site-id"`
	APIVersion        string `mapstructure:"api-version"`
}

// validateConfig is run after the configuration is loaded, and should return an error if it isn't valid.
// not checking if content-url is missing since it's optional on tableau server.
func validateConfig(ctx context.Context, cfg *config) error {
	if cfg.AccessTokenSecret == "" {
		return fmt.Errorf("access token secret is missing")
	}
	if cfg.AccessTokenName == "" {
		return fmt.Errorf("access token name is missing")
	}
	if cfg.ServerPath == "" {
		return fmt.Errorf("server path is missing")
	}

	return nil
}

// cmdFlags sets the cmdFlags required for the connector.
func cmdFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("access-token-name", "", "Name of the personal access token used to connect to the Tableau API. ($BATON_ACCESS_TOKEN_NAME)")
	cmd.PersistentFlags().String("access-token-secret", "", "Secret of the personal access token used to connect to the Tableau API. ($BATON_ACCESS_TOKEN_SECRET)")
	cmd.PersistentFlags().String("server-path", "", "Base url of your server or Tableau Cloud. ($BATON_SERVER_PATH)")
	cmd.PersistentFlags().String("site-id", "", "On server it's referred as Site ID, on cloud it appears after /site/ in the Browser address bar. ($BATON_SITE_ID)")
	cmd.PersistentFlags().String("api-version", "3.17", "API version of your server or Tableau Cloud. ($BATON_API_VERSION)")
}
