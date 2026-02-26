package main

import (
	"context"

	"github.com/conductorone/baton-sdk/pkg/config"
	cfg "github.com/conductorone/baton-tableau/pkg/config"
	"github.com/conductorone/baton-tableau/pkg/connector"
)

var version = "dev"

func main() {
	ctx := context.Background()
	config.RunConnector(
		ctx,
		"baton-tableau",
		version,
		cfg.Config,
		connector.New,
	)
}
