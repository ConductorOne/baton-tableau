package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/conductorone/baton-tableau/pkg/tableau"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
)

var (
	resourceTypeSite = &v2.ResourceType{
		Id:          "site",
		DisplayName: "Site",
	}
	resourceTypeUser = &v2.ResourceType{
		Id:          "user",
		DisplayName: "User",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_USER,
		},
		Annotations: annotationsForUserResourceType(),
	}
	resourceTypeGroup = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_GROUP,
		},
	}
	resourceTypeLicense = &v2.ResourceType{
		Id:          "license",
		DisplayName: "License",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_ROLE,
		},
	}
)

type Tableau struct {
	client                    *tableau.Client
	personalAccessTokenName   string
	personalAccessTokenSecret string
	contentUrl                string
	baseUrl                   string
}

func New(ctx context.Context, baseUrl string, contentUrl string, personalAccessTokenName string, personalAccessTokenSecret string) (*Tableau, error) {
	httpClient, err := uhttp.NewClient(ctx, uhttp.WithLogger(true, ctxzap.Extract(ctx)))
	if err != nil {
		return nil, err
	}

	credentials, err := tableau.Login(ctx, baseUrl, contentUrl, personalAccessTokenSecret, personalAccessTokenName)
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to login: %w", err)
	}

	return &Tableau{
		client:                    tableau.NewClient(credentials.Token, credentials.Site.ID, baseUrl, credentials.User.ID, httpClient),
		personalAccessTokenName:   personalAccessTokenName,
		personalAccessTokenSecret: personalAccessTokenSecret,
		contentUrl:                contentUrl,
		baseUrl:                   baseUrl,
	}, nil
}

func (tb *Tableau) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Tableau",
		Description: "Connector syncing users, groups and sites from Tableau to Baton.",
	}, nil
}

func (tb *Tableau) Validate(ctx context.Context) (annotations.Annotations, error) {
	err := tb.client.VerifyUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("tableau-connector: failed to authorize current user: %w", err)
	}

	return nil, nil
}

func (tb *Tableau) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		userBuilder(tb.client),
		siteBuilder(tb.client),
		groupBuilder(tb.client),
		licenseBuilder(tb.client),
	}
}
