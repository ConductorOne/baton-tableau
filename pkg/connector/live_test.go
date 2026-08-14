package connector

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// liveClient builds a client against a real Tableau site from the environment.
// The whole file is inert unless BATON_TABLEAU_LIVE is set, because these tests
// create and delete a real account on a real site.
func liveClient(t *testing.T) *client.Client {
	t.Helper()

	if os.Getenv("BATON_TABLEAU_LIVE") == "" {
		t.Skip("set BATON_TABLEAU_LIVE=1 to run against a live Tableau site")
	}

	c, err := client.New(context.Background(), client.Config{
		ServerPath: os.Getenv("BATON_SERVER_PATH"),
		SiteID:     os.Getenv("BATON_SITE_ID"),
		APIVersion: "3.27",
		ConnectedApp: &client.ConnectedApp{
			ClientID:    os.Getenv("BATON_CONNECTED_APP_CLIENT_ID"),
			SecretID:    os.Getenv("BATON_CONNECTED_APP_SECRET_ID"),
			SecretValue: os.Getenv("BATON_CONNECTED_APP_SECRET_VALUE"),
			Username:    os.Getenv("BATON_CONNECTED_APP_USERNAME"),
		},
	})
	require.NoError(t, err)

	return c
}

// TestLiveAccountLifecycle exercises the provisioning path a connected app is
// most likely to break on: account creation reaches for IDP discovery, which
// Tableau refuses to a JWT session, and the connector must fall back to the
// site default rather than abort. The account is created unlicensed so the run
// consumes no seat, then promoted and demoted to exercise the license path.
func TestLiveAccountLifecycle(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	users := newUserBuilder(c)
	licenses := newLicenseBuilder(c)

	// Tableau uses the address as the login name, so it must sit in a domain the
	// site will accept. Override it for sites that reject the default.
	domain := os.Getenv("BATON_TABLEAU_TEST_EMAIL_DOMAIN")
	if domain == "" {
		domain = "example.com"
	}

	email := fmt.Sprintf("baton-connector-test-%d@%s", time.Now().Unix(), domain)

	profile, err := structpb.NewStruct(map[string]any{
		"email":    email,
		"siteRole": "Unlicensed",
		"withMFA":  false,
	})
	require.NoError(t, err)

	created, _, _, err := users.CreateAccount(ctx, &v2.AccountInfo{Profile: profile}, nil)
	require.NoError(t, err, "account creation must survive Tableau refusing IDP discovery")

	success, ok := created.(*v2.CreateAccountResponse_SuccessResult)
	require.True(t, ok, "expected a success result")

	userID := success.Resource.Id.Resource
	t.Logf("created user %s (%s)", email, userID)

	// Always clean up, even if an assertion below fails.
	t.Cleanup(func() {
		if _, err := c.RemoveUserFromSite(context.Background(), userID); err != nil {
			t.Errorf("failed to remove test user %s: %v", userID, err)
		}
	})

	licenseRes, err := licenseResource("Explorer", nil, 0)
	require.NoError(t, err)

	_, err = licenses.Grant(ctx, success.Resource, &v2.Entitlement{Resource: licenseRes})
	require.NoError(t, err, "granting a license must work under a connected app")

	_, err = licenses.Revoke(ctx, &v2.Grant{Principal: success.Resource})
	require.NoError(t, err, "revoking a license must work under a connected app")

	// Group membership needs a group to write to, and this test will not invent
	// one on someone's site. Point BATON_TABLEAU_TEST_GROUP_ID at a throwaway
	// group to cover the tableau:groups:* half of the scope list as well.
	groupID := os.Getenv("BATON_TABLEAU_TEST_GROUP_ID")
	if groupID == "" {
		t.Log("BATON_TABLEAU_TEST_GROUP_ID unset; skipping group membership")
		return
	}

	groups := newGroupBuilder(c)
	groupEnt := &v2.Entitlement{Resource: &v2.Resource{
		Id: &v2.ResourceId{ResourceType: resourceTypeGroup.Id, Resource: groupID},
	}}

	_, err = groups.Grant(ctx, success.Resource, groupEnt)
	require.NoError(t, err, "adding a user to a group must work under a connected app")

	_, err = groups.Revoke(ctx, &v2.Grant{Principal: success.Resource, Entitlement: groupEnt})
	require.NoError(t, err, "removing a user from a group must work under a connected app")
}
