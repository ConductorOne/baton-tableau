package connector

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
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

// viewCapabilitySlug is the display name of the Read capability, which is the
// cheapest permission to grant and revoke on every content type.
const viewCapabilitySlug = "View"

// permissionEntitlement builds the entitlement shape the permission builders
// expect. Grant and Revoke read the capability from the third colon-separated
// field of the entitlement ID, and the target from the entitlement resource.
func permissionEntitlement(resourceType, resourceID, displaySlug string) *v2.Entitlement {
	return &v2.Entitlement{
		Id: fmt.Sprintf("%s:%s:%s", resourceType, resourceID, displaySlug),
		Resource: &v2.Resource{
			Id: &v2.ResourceId{ResourceType: resourceType, Resource: resourceID},
		},
	}
}

// TestLivePermissions covers the content permission paths against a live site:
// projects, the default workbook permissions attached to a project, workbooks,
// and views. Each capability is granted to a throwaway user and then revoked,
// so a completed run leaves the target's permissions as it found them.
//
// Every target is opt-in. Point BATON_TABLEAU_TEST_PROJECT_ID at a project you
// are willing to write to — a scratch project is the obvious choice. Views are
// only writable when their workbook has showTabs disabled; the connector
// refuses the rest by design.
func TestLivePermissions(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	projectID := os.Getenv("BATON_TABLEAU_TEST_PROJECT_ID")
	workbookID := os.Getenv("BATON_TABLEAU_TEST_WORKBOOK_ID")
	viewID := os.Getenv("BATON_TABLEAU_TEST_VIEW_ID")

	if projectID == "" && workbookID == "" && viewID == "" {
		t.Skip("set BATON_TABLEAU_TEST_PROJECT_ID, _WORKBOOK_ID or _VIEW_ID to exercise permissions")
	}

	domain := os.Getenv("BATON_TABLEAU_TEST_EMAIL_DOMAIN")
	if domain == "" {
		domain = "example.com"
	}

	user, _, err := c.AddUserToSite(ctx, client.CreateUserRequest{
		Email:    fmt.Sprintf("baton-perm-test-%d@%s", time.Now().Unix(), domain),
		SiteRole: "Viewer",
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		if _, err := c.RemoveUserFromSite(context.Background(), user.ID); err != nil {
			t.Errorf("failed to remove permission test user %s: %v", user.ID, err)
		}
	})

	principal := &v2.Resource{
		Id: &v2.ResourceId{ResourceType: resourceTypeUser.Id, Resource: user.ID},
	}

	cases := []struct {
		name         string
		resourceType string
		resourceID   string
		slug         string
		grant        func(context.Context, *v2.Resource, *v2.Entitlement) (annotations.Annotations, error)
		revoke       func(context.Context, *v2.Grant) (annotations.Annotations, error)
	}{
		{
			name: "project", resourceType: resourceTypeProject.Id, resourceID: projectID, slug: viewCapabilitySlug,
			grant: newProjectBuilder(c).Grant, revoke: newProjectBuilder(c).Revoke,
		},
		{
			// "Workbook / View" is a default workbook capability on the project,
			// which takes a different Tableau endpoint to a plain project grant.
			name: "project default workbook", resourceType: resourceTypeProject.Id, resourceID: projectID, slug: "Workbook / View",
			grant: newProjectBuilder(c).Grant, revoke: newProjectBuilder(c).Revoke,
		},
		{
			name: "workbook", resourceType: resourceTypeWorkbook.Id, resourceID: workbookID, slug: viewCapabilitySlug,
			grant: newWorkbookBuilder(c).Grant, revoke: newWorkbookBuilder(c).Revoke,
		},
		{
			name: "view", resourceType: resourceTypeView.Id, resourceID: viewID, slug: viewCapabilitySlug,
			grant: newViewBuilder(c).Grant, revoke: newViewBuilder(c).Revoke,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.resourceID == "" {
				t.Skipf("no id supplied for %s", tc.name)
			}

			entitlement := permissionEntitlement(tc.resourceType, tc.resourceID, tc.slug)

			_, err := tc.grant(ctx, principal, entitlement)
			require.NoError(t, err, "granting %s permission must work under a connected app", tc.name)

			_, err = tc.revoke(ctx, &v2.Grant{Principal: principal, Entitlement: entitlement})
			require.NoError(t, err, "revoking %s permission must work under a connected app", tc.name)
		})
	}
}
