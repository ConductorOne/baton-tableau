# `baton-tableau` [![Go Reference](https://pkg.go.dev/badge/github.com/conductorone/baton-tableau.svg)](https://pkg.go.dev/github.com/conductorone/baton-tableau) ![ci](https://github.com/conductorone/baton-tableau/actions/workflows/ci.yaml/badge.svg)

`baton-tableau` is a connector for Tableau built using the [Baton SDK](https://github.com/conductorone/baton-sdk).

Check out [Baton](https://github.com/conductorone/baton) to learn more about the project in general.

# Prerequisites

- **Tableau Cloud** or **Tableau Server** with REST API access
- A user account with **Site Administrator Explorer** or **Site Administrator Creator** role
- A **Personal Access Token (PAT)** generated from that administrator account

# Getting Started

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-tableau
baton-tableau --access-token-name your-token-name --access-token-secret your-token-secret --server-path us-east-1.online.tableau.com --site-id your-site-id
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_ACCESS_TOKEN_NAME=your-token-name -e BATON_ACCESS_TOKEN_SECRET=your-token-secret -e BATON_SERVER_PATH=us-east-1.online.tableau.com -e BATON_SITE_ID=your-site-id ghcr.io/conductorone/baton-tableau:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-tableau/cmd/baton-tableau@main

baton-tableau --access-token-name your-token-name --access-token-secret your-token-secret --server-path us-east-1.online.tableau.com --site-id your-site-id

baton resources
```

# Data Model

`baton-tableau` will pull down information about the following resources:

- **Sites** — The Tableau site the connector is authenticated against (top-level resource)
- **Users** — All users on the site with email, site role, auth setting, and last login
- **Groups** — Tableau groups with membership information
- **Licenses** — License tiers (Creator, Explorer, Viewer, Unlicensed) with role-based assignment
- **Projects** — Tableau projects with Read/Write permission assignments for users and groups
- **Workbooks** — Tableau workbooks with 15 granular permission assignments for users and groups
- **Views** — Individual dashboards/views with granular permissions (inherited from workbook when `showTabs=true`)

`baton-tableau` supports the following provisioning operations:

| Resource Type | Operation | Description |
|---------------|-----------|-------------|
| Users | Create | Create new users with email, site role, and IDP/MFA authentication |
| Users | Delete | Remove users from the site |
| Site Roles | Grant/Revoke | Assign/remove site-wide roles (Creator, Explorer, SiteAdministratorCreator, etc.) |
| Licenses | Grant/Revoke | Assign/remove license tiers by updating site role |
| Groups | Grant/Revoke | Add/remove users from groups |
| Projects | Grant/Revoke | Assign/remove Read, Write permissions for users and groups |
| Workbooks | Grant/Revoke | Assign/remove 15 granular permissions for users and groups |
| Views | Grant/Revoke | Assign/remove view permissions for users and groups (when `showTabs=false`) |

## Important Notes

- **Connection validation**: The connector validates credentials on startup by calling `GetSite()`. Invalid credentials or unreachable servers fail immediately with a clear error.
- **showTabs**: When a workbook has `showTabs=true`, view-level permissions are inherited from the workbook and cannot be modified independently. Grant/Revoke on those views are blocked with a descriptive error.
- **Views in Personal Space**: Workbooks in a user's Personal Space (not under any project) are not synced, and therefore their views are not synced either.
- **Server Administrators**: Users with `ServerAdministrator` site role are skipped during group membership sync (server-level admins are not site-scoped).
- **Grant expansion**: Group-based permissions on projects, workbooks, and views are supported. When a group has a permission, all members inherit it.
- **"All Users" group**: Tableau automatically adds every user to the built-in "All Users" group. This group cannot be modified via the API — Grant and Revoke operations on it are not supported. The connector syncs its membership to show each user's base site role.
- **License revoke constraints**: Revoking a license (setting a user to "Unlicensed") will fail if the user belongs to a group with a "Grant role on sign in" minimum site role. Remove the user from the constraining group first, then revoke the license.
- **Idempotency**: Granting a permission that already exists returns success (not an error). Revoking a permission that is already removed also returns success.

# Credentials Setup

## Personal Access Token (PAT)

1. Sign in to your Tableau Cloud or Tableau Server instance
2. Click on your profile icon in the top-right corner and select **"My Account Settings"**
3. Scroll down to the **"Personal Access Tokens"** section
4. Enter a **Token Name** and click **"Create new token"**
5. Copy the **Token Secret** immediately — it is displayed only once

> **Important**: The user account must have **Site Administrator Explorer** (read-only sync) or **Site Administrator Creator** (sync + provisioning) role. PAT creation must be enabled by a site administrator.

**Documentation:**
- Tableau Cloud: https://help.tableau.com/current/online/en-us/security_personal_access_tokens.htm
- Tableau Server: https://help.tableau.com/current/server/en-us/security_personal_access_tokens.htm

## Configuration Flags

| Flag | Required | Description |
|------|----------|-------------|
| `--access-token-name` | Yes | Name of the Personal Access Token |
| `--access-token-secret` | Yes | Secret value of the Personal Access Token |
| `--server-path` | Yes | Base URL **without** `/api/<version>` suffix. Examples: `us-east-1.online.tableau.com` (Cloud), `your-server-hostname` (Server) |
| `--site-id` | No | Content URL of the site (e.g., `mycompany`). Can be found after `/site/` in the browser URL. Leave empty for the default site on Tableau Server |
| `--api-version` | No | Tableau REST API version (default: `3.27`). Can be changed to match your server's supported version — see [API version reference](https://help.tableau.com/current/api/rest_api/en-us/REST/rest_api_concepts_versions.htm) |

## Account Creation Schema

When creating users via provisioning, the `--create-account-profile` JSON accepts:

| Field | Required | Description |
|-------|----------|-------------|
| `email` | Yes | Email address used as the user's login name |
| `siteRole` | Yes | Site role: `Creator`, `Explorer`, `ExplorerCanPublish`, `SiteAdministratorExplorer`, `SiteAdministratorCreator`, `Viewer`, `Unlicensed`. See [Tableau REST API Site Roles](https://help.tableau.com/current/api/rest_api/en-us/REST/rest_api_concepts_new_site_roles.htm) |
| `withMFA` | No | If `true`, creates user with `TableauIDWithMFA` authentication. Default: `false` (uses SAML IDP) |
| `idpConfigurationName` | No | Name of a specific SAML IDP when multiple are configured. Auto-selected if only one exists |

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
      --access-token-name string     required: Access token name used to connect to the Tableau API ($BATON_ACCESS_TOKEN_NAME)
      --access-token-secret string   required: Access token secret used to connect to the Tableau API ($BATON_ACCESS_TOKEN_SECRET)
      --api-version string           API version of your Tableau Server or Tableau Cloud instance ($BATON_API_VERSION) (default "3.27")
      --client-id string             The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string         The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
  -f, --file string                  The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                         help for baton-tableau
      --log-format string            The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string             The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -p, --provisioning                 This must be set in order for provisioning actions to be enabled ($BATON_PROVISIONING)
      --server-path string           required: Base URL of your Tableau Server or Tableau Cloud instance ($BATON_SERVER_PATH)
      --site-id string               Site ID (content URL) of the Tableau site to connect to ($BATON_SITE_ID)
      --ticketing                    This must be set to enable ticketing support ($BATON_TICKETING)
  -v, --version                      version for baton-tableau

Use "baton-tableau [command] --help" for more information about a command.
```
