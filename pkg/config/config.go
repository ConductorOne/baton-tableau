package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	AccessTokenName = field.StringField(
		"access-token-name",
		field.WithDisplayName("Access Token Name"),
		field.WithDescription("Access token name used to connect to the Tableau API. Required unless connected app credentials are supplied"),
	)

	AccessTokenSecret = field.StringField(
		"access-token-secret",
		field.WithDisplayName("Access Token Secret"),
		field.WithDescription("Access token secret used to connect to the Tableau API. Required unless connected app credentials are supplied"),
		field.WithIsSecret(true),
	)

	ConnectedAppClientID = field.StringField(
		"connected-app-client-id",
		field.WithDisplayName("Connected App Client ID"),
		field.WithDescription("Client ID of a Tableau connected app configured for direct trust. Use instead of a personal access token"),
	)

	ConnectedAppSecretID = field.StringField(
		"connected-app-secret-id",
		field.WithDisplayName("Connected App Secret ID"),
		field.WithDescription("Secret ID of the Tableau connected app"),
	)

	ConnectedAppSecretValue = field.StringField(
		"connected-app-secret-value",
		field.WithDisplayName("Connected App Secret Value"),
		field.WithDescription("Secret value of the Tableau connected app"),
		field.WithIsSecret(true),
	)

	ConnectedAppUsername = field.StringField(
		"connected-app-username",
		field.WithDisplayName("Connected App Username"),
		field.WithDescription("Email address of the Tableau user the connector acts as. Needs the same site administrator rights as a personal access token owner"),
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
		field.WithDefaultValue("3.27"),
	)

	BaseURL = field.StringField(
		"base-url",
		field.WithDisplayName("Base URL"),
		field.WithDescription("Override the Tableau API URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)

	ConfigurationFields = []field.SchemaField{
		AccessTokenName,
		AccessTokenSecret,
		ConnectedAppClientID,
		ConnectedAppSecretID,
		ConnectedAppSecretValue,
		ConnectedAppUsername,
		ServerPath,
		SiteID,
		APIVersion,
		BaseURL,
	}

	// Credentials come as one complete set or the other. Naming a single field
	// from each group in the exclusivity and presence constraints is enough,
	// because the two RequiredTogether rules force each group to be whole.
	ConfigurationConstraints = []field.SchemaFieldRelationship{
		field.FieldsRequiredTogether(AccessTokenName, AccessTokenSecret),
		field.FieldsRequiredTogether(
			ConnectedAppClientID,
			ConnectedAppSecretID,
			ConnectedAppSecretValue,
			ConnectedAppUsername,
		),
		field.FieldsAtLeastOneUsed(AccessTokenName, ConnectedAppClientID),
		field.FieldsMutuallyExclusive(AccessTokenName, ConnectedAppClientID),
	}
)

//go:generate go run -tags=generate ./gen
var Config = field.NewConfiguration(
	ConfigurationFields,
	field.WithConstraints(ConfigurationConstraints...),
	field.WithConnectorDisplayName("Tableau"),
	field.WithHelpUrl("/docs/baton/tableau"),
	field.WithIconUrl("/static/app-icons/tableau.svg"),
)
