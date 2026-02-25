package client

import (
	"fmt"
	"time"
)

type Credentials struct {
	Site                      Site   `json:"site"`
	User                      User   `json:"user"`
	Token                     string `json:"token"`
	EstimatedTimeToExpiration string `json:"estimatedTimeToExpiration"`
}

type Site struct {
	ID         string `json:"id"`
	ContentURL string `json:"contentUrl"`
	Name       string `json:"name"`
}

type User struct {
	Email       string    `json:"email"`
	ID          string    `json:"id"`
	FullName    string    `json:"fullName"`
	Name        string    `json:"name"`
	SiteRole    string    `json:"siteRole"`
	AuthSetting string    `json:"authSetting"`
	LastLogin   *time.Time `json:"lastLogin"`
}

type CreateUserRequest struct {
	// has to be name in the payload
	Email              string `json:"name"`
	SiteRole           string `json:"siteRole"`
	AuthSetting        string `json:"authSetting,omitempty"`
	IdpConfigurationId string `json:"idpConfigurationId,omitempty"`
}

type IdpConfiguration struct {
	IdpConfigurationId   string `json:"idpConfigurationId"`
	IdpConfigurationName string `json:"idpConfigurationName"`
	AuthSetting          string `json:"authSetting"`
	Enabled              bool   `json:"enabled"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Project struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	ContentPermissions string    `json:"contentPermissions"`
	Owner              *OwnerRef `json:"owner,omitempty"`
}

type Workbook struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	ContentURL string      `json:"contentUrl"`
	ShowTabs   string      `json:"showTabs"`
	Project    *ProjectRef `json:"project,omitempty"`
	Owner      *OwnerRef   `json:"owner,omitempty"`
}

type View struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	ContentURL string       `json:"contentUrl"`
	Workbook   *WorkbookRef `json:"workbook,omitempty"`
}

type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type WorkbookRef struct {
	ID string `json:"id"`
}

type OwnerRef struct {
	ID string `json:"id"`
}

type GranteeCapabilities struct {
	User         *UserRef     `json:"user,omitempty"`
	Group        *GroupRef    `json:"group,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

type UserRef struct {
	ID string `json:"id"`
}

type Pagination struct {
	PageNumber     string `json:"pageNumber"`
	PageSize       string `json:"pageSize"`
	TotalAvailable string `json:"totalAvailable"`
}

type GroupRef struct {
	ID string `json:"id"`
}

type Capabilities struct {
	Capability []*Capability `json:"capability"`
}

type Capability struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}

// =============================================================================
// API Response Types
// =============================================================================

type credentialsResponse struct {
	Credentials Credentials `json:"credentials"`
}

type siteResponse struct {
	Site Site `json:"site"`
}

type userResponse struct {
	User User `json:"user"`
}

type createUserResponse struct {
	User *User `json:"user"`
}

type usersResponse struct {
	Pagination Pagination `json:"pagination"`
	Users      struct {
		User []*User `json:"user"`
	} `json:"users"`
}

type groupsResponse struct {
	Pagination Pagination `json:"pagination"`
	Groups     struct {
		Group []*Group `json:"group"`
	} `json:"groups"`
}

type projectsResponse struct {
	Pagination Pagination `json:"pagination"`
	Projects   struct {
		Project []*Project `json:"project"`
	} `json:"projects"`
}

type workbooksResponse struct {
	Pagination Pagination `json:"pagination"`
	Workbooks  struct {
		Workbook []*Workbook `json:"workbook"`
	} `json:"workbooks"`
}

type workbookResponse struct {
	Workbook Workbook `json:"workbook"`
}

type viewsResponse struct {
	Pagination Pagination `json:"pagination"`
	Views      struct {
		View []*View `json:"view"`
	} `json:"views"`
}

type permissionsResponse struct {
	Permissions struct {
		GranteeCapabilities []*GranteeCapabilities `json:"granteeCapabilities"`
	} `json:"permissions"`
}

type idpConfigurationsResponse struct {
	SiteAuthConfigurations struct {
		SiteAuthConfiguration []*IdpConfiguration `json:"siteAuthConfiguration"`
	} `json:"siteAuthConfigurations"`
}

// tableauErrorResponse represents the error response structure from the Tableau REST API.
// Example: {"error":{"summary":"Bad Request","detail":"You cannot remove the user...","code":"0x5CE10192"}}.
type tableauErrorResponse struct {
	Error *tableauError `json:"error"`
}

type tableauError struct {
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
	Code    string `json:"code"`
}

func (e *tableauError) String() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s (code: %s)", e.Summary, e.Detail, e.Code)
	}
	return fmt.Sprintf("%s (code: %s)", e.Summary, e.Code)
}
