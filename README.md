`baton-tableau` is a connector for Tableau built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the Tableau API to sync data about sites, users, and groups.

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

# Getting Started
## Prerequisites
1. Join the [Tableau Developer Program](https://www.tableau.com/developer) (or use your own server). This will allow you to work with
   Tableau API. 
2. Personal Access Token Name and Personal Access Token Secret. These are needed to login to the site. You can create them under `My Account Settings - Personal Access Tokens`
3. Server path parameter. More info [here](https://help.tableau.com/current/api/rest_api/en-us/REST/rest_api_concepts_auth.htm#the-sign-in-uri). 
4. Site ID (Content URL). More info [here](https://help.tableau.com/current/api/rest_api/en-us/REST/rest_api_concepts_auth.htm#the-site-attribute).


## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-tableau
baton-tableau
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_SITE_NAME=siteName BATON_SITE_URL=siteUrl BATON_ACCESS_TOKEN_SECRET=accessTokenSecret BATON_ACCESS_TOKEN_NAME=accessTokenName ghcr.io/conductorone/baton-tableau:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-tableau/cmd/baton-tableau@main

BATON_SITE_NAME=siteName BATON_SITE_URL=siteUrl BATON_ACCESS_TOKEN_SECRET=accessTokenSecret BATON_ACCESS_TOKEN_NAME=accessTokenName
baton resources
```

# Data Model

`baton-tableau` will pull down information about the following Tableau resources:
- Sites
- Users
- Groups
- Licenses

# Contributing, Support and Issues

We started Baton because we were tired of taking screenshots and manually building spreadsheets. We welcome contributions, and ideas, no matter how small -- our goal is to make identity and permissions sprawl less painful for everyone. If you have questions, problems, or ideas: Please open a Github Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-tableau` Command Line Usage

```
baton-tableau

Usage:
  baton-tableau [flags]
  baton-tableau [command]

Available Commands:
  capabilities       Get connector capabilities
  completion         Generate the autocompletion script for the specified shell
  help               Help about any command

Flags:
      --access-token-name string     Name of the personal access token used to connect to the Tableau API. ($BATON_ACCESS_TOKEN_NAME)
      --access-token-secret string   Secret of the personal access token used to connect to the Tableau API. ($BATON_ACCESS_TOKEN_SECRET)
      --api-version string           API version of your server or Tableau Cloud. ($BATON_API_VERSION) (default "3.17")
      --client-id string             The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string         The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
  -f, --file string                  The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                         help for baton-tableau
      --log-format string            The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string             The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning                 This must be set in order for provisioning actions to be enabled. ($BATON_PROVISIONING)
      --server-path string           Base url of your server or Tableau Cloud. ($BATON_SERVER_PATH)
      --site-id string               On server it's referred as Site ID, on cloud it appears after /site/ in the Browser address bar. ($BATON_SITE_ID)
  -v, --version                      version for baton-tableau

Use "baton-tableau [command] --help" for more information about a command.
```
