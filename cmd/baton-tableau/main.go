package main

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/field"
	"github.com/conductorone/baton-sdk/pkg/types"

	"github.com/conductorone/baton-sdk/pkg/config"
	cfg "github.com/conductorone/baton-tableau/pkg/config"
	"github.com/conductorone/baton-tableau/pkg/connector"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
)

var version = "dev"

func main() {
	ctx := context.Background()

	_, cmd, err := config.DefineConfiguration(
		ctx,
		"baton-tableau",
		getConnector[*cfg.Tableau],
		cfg.Config,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cmd.Version = version

	err = cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func getConnector[T field.Configurable](ctx context.Context, config T) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)
	if err := field.Validate(cfg.Config, config); err != nil {
		return nil, err
	}

	apiVersion := config.GetString(cfg.APIVersion.FieldName)
	if apiVersion == "" {
		apiVersion = "3.17"
	}

	serverPath := config.GetString(cfg.ServerPath.FieldName)
	baseUrl, err := url.JoinPath("https://", serverPath, "api", apiVersion)
	if err != nil {
		l.Error("error creating base url", zap.Error(err))
	}

	cb, err := connector.New(
		ctx,
		baseUrl,
		config.GetString(cfg.SiteID.FieldName),
		config.GetString(cfg.AccessTokenName.FieldName),
		config.GetString(cfg.AccessTokenSecret.FieldName),
	)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	c, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		l.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	return c, nil
}
