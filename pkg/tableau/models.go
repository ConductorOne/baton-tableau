package tableau

import "time"

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
	LastLogin   time.Time `json:"lastLogin"`
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

type View struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ContentURL string `json:"contentUrl"`
}

type GranteeCapabilities struct {
	User         *UserRef     `json:"user,omitempty"`
	Group        *GroupRef    `json:"group,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

type UserRef struct {
	ID string `json:"id"`
}

type GroupRef struct {
	ID string `json:"id"`
}

type Capabilities struct {
	Capability []Capability `json:"capability"`
}

type Capability struct {
	Name string `json:"name"`
	Mode string `json:"mode"`
}
