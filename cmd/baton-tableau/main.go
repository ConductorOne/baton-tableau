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
		getConnector,
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

func getConnector(ctx context.Context, tc *cfg.Tableau) (types.ConnectorServer, error) {
	l := ctxzap.Extract(ctx)
	if err := field.Validate(cfg.Config, tc); err != nil {
		return nil, err
	}

	apiVersion := tc.ApiVersion
	if apiVersion == "" {
		apiVersion = "3.17"
	}

	serverPath := tc.ServerPath
	baseUrl, err := url.JoinPath("https://", serverPath, "api", apiVersion)
	if err != nil {
		l.Error("error creating base url", zap.Error(err))
	}

	cb, err := connector.New(
		ctx,
		baseUrl,
		tc.SiteId,
		tc.AccessTokenName,
		tc.AccessTokenSecret,
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
