package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	AccessTokenName = field.StringField(
		"access-token-name",
		field.WithRequired(true),
		field.WithDisplayName("Access Token Name"),
		field.WithDescription("Access token name used to connect to the Tableau API"),
	)

	AccessTokenSecret = field.StringField(
		"access-token-secret",
		field.WithRequired(true),
		field.WithDisplayName("Access Token Secret"),
		field.WithDescription("Access token secret used to connect to the Tableau API"),
		field.WithIsSecret(true),
	)

	ServerPath = field.StringField(
		"server-path",
		field.WithRequired(true),
		field.WithDisplayName("Server Path"),
		field.WithDescription("Base URL of your Tableau Server or Tableau Cloud instance"),
	)

	SiteID = field.StringField(
		"site-id",
		field.WithDisplayName("Site ID"),
		field.WithDescription("Site ID (content URL) of the Tableau site to connect to"),
	)

	APIVersion = field.StringField(
		"api-version",
		field.WithDisplayName("API Version"),
		field.WithDescription("API version of your Tableau Server or Tableau Cloud instance"),
	)

	ConfigurationFields = []field.SchemaField{
		AccessTokenName,
		AccessTokenSecret,
		ServerPath,
		SiteID,
		APIVersion,
	}
)

//go:generate go run -tags=generate ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConnectorDisplayName("Tableau"),
	field.WithHelpUrl("/docs/baton/tableau"),
	field.WithIconUrl("/static/app-icons/tableau.svg"),
)
