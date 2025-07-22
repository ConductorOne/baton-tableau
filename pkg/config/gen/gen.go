package main

import (
	"github.com/conductorone/baton-sdk/pkg/config"
	cfg "github.com/conductorone/baton-tableau/pkg/config"
)

func main() {
	config.Generate("tableau", cfg.Config)
}
