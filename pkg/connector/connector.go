package connector

import (
	"context"
	"fmt"
	"io"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-tableau/pkg/client"
)

type Connector struct {
	client *client.Client
}

func (c *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		newUserBuilder(c.client),
		newSiteBuilder(c.client),
		newGroupBuilder(c.client),
		newLicenseBuilder(c.client),
		newProjectBuilder(c.client),
		newWorkbookBuilder(c.client),
		newViewBuilder(c.client),
	}
}

func (c *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

func New(ctx context.Context, serverPath, siteID, accessTokenName, accessTokenSecret, apiVersion string) (*Connector, error) {
	tableauClient, err := client.New(ctx, serverPath, siteID, accessTokenName, accessTokenSecret, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &Connector{client: tableauClient}, nil
}

func (c *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Tableau",
		Description: "Connector syncing users, groups, sites, licenses, projects, workbooks, and views from Tableau to Baton.",
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				"email": {
					DisplayName: "Email",
					Required:    true,
					Description: "The email address of the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Email",
					Order:       1,
				},
				"siteRole": {
					DisplayName: "Site Role",
					Required:    true,
					Description: `The role to assign to the user on the site. Possible values are:
					Creator, Explorer, ExplorerCanPublish, SiteAdministratorExplorer, SiteAdministratorCreator, Unlicensed, or Viewer.`,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Site Role",
					Order:       2,
				},
				"withMFA": {
					DisplayName: "With MFA",
					Required:    false,
					Description: `If true, creates users with TableauIDWithMFA authentication instead of using the site's IDP configuration (SAML). Defaults to false.`,
					Field: &v2.ConnectorAccountCreationSchema_Field_BoolField{
						BoolField: &v2.ConnectorAccountCreationSchema_BoolField{},
					},
					Order: 3,
				},
				"idpConfigurationName": {
					DisplayName: "IDP Configuration Name",
					Required:    false,
					Description: `The name of the SAML IDP configuration to use for user authentication. 
						Only required when multiple SAML IDP configurations are enabled on the site.
						If only one SAML IDP exists, it will be used automatically. 
						If no SAML IDPs exist, set withMFA=true instead.`,
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "IDP Configuration Name",
					Order:       4,
				},
			},
		},
	}, nil
}

func (c *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}
