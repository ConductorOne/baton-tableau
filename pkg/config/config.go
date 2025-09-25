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
		field.WithDescription("On Tableau Server, this is referred to as Site ID. On Tableau Cloud"),
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

	// FieldRelationships defines relationships between the ConfigurationFields that can be automatically validated.
	// For example, a username and password can be required together, or an access token can be
	// marked as mutually exclusive from the username password pair.
	FieldRelationships = []field.SchemaFieldRelationship{}
)

//go:generate go run -tags=generate ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConstraints(FieldRelationships...),
	field.WithConnectorDisplayName("Tableau"),
	field.WithHelpUrl("/docs/baton/tableau"),
	field.WithIconUrl("/static/app-icons/tableau.svg"),
)
