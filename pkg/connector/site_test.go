package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	grant "github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const siteTestSiteID = "site-1"

// siteTestUsers is the mock Tableau user store behind newSiteTestClient. Tests
// change a role out-of-band through set, the way an admin editing the user in
// the Tableau UI would.
type siteTestUsers struct {
	mu    sync.Mutex
	roles map[string]string
	puts  []recordedPut
	gets  int
	// getStatus / putStatus, when non-zero, make that verb fail with the status.
	getStatus int
	putStatus int
}

func (u *siteTestUsers) set(userID, role string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.roles[userID] = role
}

func (u *siteTestUsers) recordedPuts() []recordedPut {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]recordedPut(nil), u.puts...)
}

// newSiteTestClient starts a mock Tableau API that answers with literal wire
// JSON. GetUser reads the current role from users, a PUT stores the requested
// siteRole and is recorded, and an unknown user ID is a 404.
func newSiteTestClient(t *testing.T, users *siteTestUsers) *client.Client {
	t.Helper()

	usersPathPrefix := "/sites/" + siteTestSiteID + "/users/"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/signin":
			_, _ = w.Write([]byte(`{"credentials":{"site":{"id":"` + siteTestSiteID + `"},"user":{"id":"me"},"token":"test-token"}}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, usersPathPrefix):
			userID := strings.TrimPrefix(r.URL.Path, usersPathPrefix)
			users.mu.Lock()
			role, ok := users.roles[userID]
			users.gets++
			failStatus := users.getStatus
			users.mu.Unlock()
			if failStatus != 0 {
				w.WriteHeader(failStatus)
				_, _ = w.Write([]byte(`{"error":{"summary":"Internal Error","detail":"boom","code":"500000"}}`))
				return
			}
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"summary":"Resource Not Found","detail":"User '` + userID + `' does not exist.","code":"404017"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"user":{"id":%q,"name":"jdoe","siteRole":%q,"authSetting":"ServerDefault"}}`, userID, role)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, usersPathPrefix):
			userID := strings.TrimPrefix(r.URL.Path, usersPathPrefix)
			var body struct {
				User struct {
					SiteRole string `json:"siteRole"`
				} `json:"user"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			users.mu.Lock()
			if failStatus := users.putStatus; failStatus != 0 {
				users.mu.Unlock()
				w.WriteHeader(failStatus)
				_, _ = w.Write([]byte(`{"error":{"summary":"Internal Error","detail":"boom","code":"500000"}}`))
				return
			}
			_, ok := users.roles[userID]
			if ok {
				users.puts = append(users.puts, recordedPut{userID: userID, siteRole: body.User.SiteRole})
				users.roles[userID] = body.User.SiteRole
			}
			users.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"summary":"Resource Not Found","detail":"User '` + userID + `' does not exist.","code":"404017"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"user":{"name":"jdoe","siteRole":%q,"authSetting":"ServerDefault"}}`, body.User.SiteRole)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"summary":"Not Found","detail":"unexpected request","code":"0x2"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := client.New(context.Background(), "unused", siteTestSiteID, "token-name", "token-secret", "3.19", srv.URL)
	require.NoError(t, err)
	return c
}

func newSiteTestUsers(roles map[string]string) *siteTestUsers {
	return &siteTestUsers{roles: roles}
}

func testSiteResource() *v2.Resource {
	return &v2.Resource{
		Id:          &v2.ResourceId{ResourceType: resourceTypeSite.Id, Resource: siteTestSiteID},
		DisplayName: "Site 1",
	}
}

// testSiteEntitlement builds the entitlement the Entitlements sync emits for an
// API role name such as "Viewer".
func testSiteEntitlement(apiRole string) *v2.Entitlement {
	return ent.NewPermissionEntitlement(testSiteResource(), roles[apiRole], ent.WithGrantableTo(resourceTypeUser))
}

func testSiteGrant(apiRole, userID string) *v2.Grant {
	return &v2.Grant{
		Entitlement: testSiteEntitlement(apiRole),
		Principal:   testUserPrincipal(userID),
	}
}

// testSiteGrantID is the ID of the site-role grant a user holds for apiRole.
func testSiteGrantID(userID, apiRole string) string {
	return grant.NewGrantID(testUserPrincipal(userID).Id, testSiteEntitlement(apiRole))
}

// TestSiteGrant_IdenticalRole asserts that granting the role the user already
// holds writes nothing and reports GrantAlreadyExists.
func TestSiteGrant_IdenticalRole(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Grant(context.Background(), testUserPrincipal("user-1"), testSiteEntitlement(viewer))
	require.NoError(t, err)
	require.Empty(t, users.recordedPuts(), "identical-role grant must not write")
	require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
}

// TestSiteGrant_ReplacesPriorSiteRole asserts that replacing one non-admin role
// with another writes once and reports GrantReplaced naming the user's prior
// site-role grant, not a license grant.
func TestSiteGrant_ReplacesPriorSiteRole(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Grant(context.Background(), testUserPrincipal("user-1"), testSiteEntitlement(explorer))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: explorer}}, users.recordedPuts())

	replaced := &v2.GrantReplaced{}
	ok, err := annos.Pick(replaced)
	require.NoError(t, err)
	require.True(t, ok, "replacing a held site role must report GrantReplaced")
	require.Equal(t, testSiteGrantID("user-1", viewer), replaced.GetReplacedGrantId())
	require.True(t, strings.HasPrefix(replaced.GetReplacedGrantId(), resourceTypeSite.Id+":"), "replaced grant must be the site-role grant, not a license grant")
}

// TestSiteGrant_UserNotFound asserts that granting to a missing user is a
// NotFound error and writes nothing.
func TestSiteGrant_UserNotFound(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{})
	s := newSiteBuilder(newSiteTestClient(t, users))

	_, err := s.Grant(context.Background(), testUserPrincipal("ghost"), testSiteEntitlement(viewer))
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, users.recordedPuts())
}

// TestSiteRevoke_StaleRole asserts that revoking a role the user no longer
// holds is a no-op and does not wipe the role they hold now.
func TestSiteRevoke_StaleRole(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": creator})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Revoke(context.Background(), testSiteGrant(viewer, "user-1"))
	require.NoError(t, err)
	require.Empty(t, users.recordedPuts(), "stale revoke must not write")
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	require.Equal(t, creator, users.roles["user-1"], "the user's current role must survive a stale revoke")
}

// TestSiteRevoke_UnlicensedHeld asserts that revoking Unlicensed from a user who
// currently holds it is a GrantAlreadyRevoked no-op: it is the floor role and
// there is nothing below it to write.
func TestSiteRevoke_UnlicensedHeld(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": unlicensed})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Revoke(context.Background(), testSiteGrant(unlicensed, "user-1"))
	require.NoError(t, err)
	require.Empty(t, users.recordedPuts(), "revoking the floor role must not write")
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	require.Equal(t, unlicensed, users.roles["user-1"])
}

// TestSiteRevoke_HeldRole asserts that revoking the exact licensed role the
// user holds resets them to Unlicensed.
func TestSiteRevoke_HeldRole(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": explorer})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Revoke(context.Background(), testSiteGrant(explorer, "user-1"))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: unlicensed}}, users.recordedPuts())
	require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
}

// TestSiteRevoke_UserNotFound asserts that revoking from a missing user is
// GrantAlreadyRevoked without a write.
func TestSiteRevoke_UserNotFound(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Revoke(context.Background(), testSiteGrant(viewer, "ghost"))
	require.NoError(t, err)
	require.Empty(t, users.recordedPuts())
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
}

// TestSite_GrantThenRevokeSameClient asserts that on one client a revoke
// following a grant sees the role the grant wrote, not a cached earlier read.
func TestSite_GrantThenRevokeSameClient(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	annos, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(creator))
	require.NoError(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: creator}}, users.recordedPuts())

	annos, err = s.Revoke(ctx, testSiteGrant(creator, "user-1"))
	require.NoError(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}), "revoke must see the role the grant just wrote")
	require.Equal(t, []recordedPut{
		{userID: "user-1", siteRole: creator},
		{userID: "user-1", siteRole: unlicensed},
	}, users.recordedPuts())
}

// TestSite_RoleChangedBetweenGrants asserts that a second grant on the same
// client sees a role changed out-of-band after the first read.
func TestSite_RoleChangedBetweenGrants(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	annos, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(viewer))
	require.NoError(t, err)
	require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
	require.Empty(t, users.recordedPuts())

	users.set("user-1", creator)

	annos, err = s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(viewer))
	require.NoError(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}), "grant must see the externally changed role")
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: viewer}}, users.recordedPuts())

	replaced := &v2.GrantReplaced{}
	ok, err := annos.Pick(replaced)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testSiteGrantID("user-1", creator), replaced.GetReplacedGrantId())
}

// TestSite_RoleChangedBetweenGrantAndRevoke asserts that a revoke following an
// out-of-band role change on the same client leaves the new role alone.
func TestSite_RoleChangedBetweenGrantAndRevoke(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	_, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(creator))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: creator}}, users.recordedPuts())

	users.set("user-1", explorer)

	annos, err := s.Revoke(ctx, testSiteGrant(creator, "user-1"))
	require.NoError(t, err)
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	require.Len(t, users.recordedPuts(), 1, "revoke of a role no longer held must not write")
	require.Equal(t, explorer, users.roles["user-1"])
}

// TestSite_ReadErrorPropagates asserts that a non-404 read failure aborts Grant
// and Revoke without a write and is not mistaken for NotFound.
func TestSite_ReadErrorPropagates(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	users.getStatus = http.StatusInternalServerError
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	_, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(creator))
	require.Error(t, err)
	require.NotEqual(t, codes.NotFound, status.Code(err))

	annos, err := s.Revoke(ctx, testSiteGrant(viewer, "user-1"))
	require.Error(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}), "a read failure must not look like an already-revoked grant")
	require.Empty(t, users.recordedPuts())
}

// TestSite_WriteErrorPropagates asserts that a failed PUT is returned as an
// error with no success annotations.
func TestSite_WriteErrorPropagates(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	users.putStatus = http.StatusInternalServerError
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	annos, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(creator))
	require.Error(t, err)
	require.False(t, annos.Contains(&v2.GrantReplaced{}))
	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))

	annos, err = s.Revoke(ctx, testSiteGrant(viewer, "user-1"))
	require.Error(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	require.Equal(t, viewer, users.roles["user-1"])
}

// TestSiteGrant_InvalidTargets asserts that unassignable or malformed targets
// are rejected before any read or write.
func TestSiteGrant_InvalidTargets(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	_, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(serverAdministrator))
	require.Error(t, err, "ServerAdministrator is not assignable")

	_, err = s.Grant(ctx, testUserPrincipal("user-1"), &v2.Entitlement{Id: "site:site-1:bogus"})
	require.Error(t, err, "unknown role slug")

	_, err = s.Grant(ctx, testUserPrincipal("user-1"), &v2.Entitlement{Id: "malformed"})
	require.Error(t, err, "malformed entitlement ID")

	_, err = s.Revoke(ctx, &v2.Grant{Entitlement: &v2.Entitlement{Id: "site:site-1:bogus"}, Principal: testUserPrincipal("user-1")})
	require.Error(t, err, "unknown role slug on revoke")

	require.Empty(t, users.recordedPuts())
	require.Zero(t, users.gets, "invalid targets must be rejected before reading")
}

// TestSiteGrant_ReplaceUnknownPriorRole asserts that a prior role outside the
// known site roles is replaced without a GrantReplaced annotation.
func TestSiteGrant_ReplaceUnknownPriorRole(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": "SomeFutureRole"})
	s := newSiteBuilder(newSiteTestClient(t, users))

	annos, err := s.Grant(context.Background(), testUserPrincipal("user-1"), testSiteEntitlement(viewer))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: viewer}}, users.recordedPuts())
	require.False(t, annos.Contains(&v2.GrantReplaced{}))
}

// TestSiteGrant_ExactRoleNoHierarchy asserts that Grant compares roles exactly:
// a role that "includes" the requested one (SiteAdministratorCreator vs Creator)
// is still replaced, and the same exact role (including Unlicensed) is a no-op.
func TestSiteGrant_ExactRoleNoHierarchy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, held, target string
		wantPut            bool
	}{
		{"viewer to creator", viewer, creator, true},
		{"creator to viewer", creator, viewer, true},
		{"site admin creator to creator", siteAdministratorCreator, creator, true},
		{"site admin to viewer", siteAdministrator, viewer, true},
		{"site admin explorer to unlicensed", siteAdministratorExplorer, unlicensed, true},
		{"unlicensed to unlicensed", unlicensed, unlicensed, false},
		{"site admin creator to site admin creator", siteAdministratorCreator, siteAdministratorCreator, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			users := newSiteTestUsers(map[string]string{"user-1": tc.held})
			s := newSiteBuilder(newSiteTestClient(t, users))

			annos, err := s.Grant(context.Background(), testUserPrincipal("user-1"), testSiteEntitlement(tc.target))
			require.NoError(t, err)
			if !tc.wantPut {
				require.Empty(t, users.recordedPuts())
				require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
				require.False(t, annos.Contains(&v2.GrantReplaced{}))
				return
			}
			require.Equal(t, []recordedPut{{userID: "user-1", siteRole: tc.target}}, users.recordedPuts())
			require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
			replaced := &v2.GrantReplaced{}
			ok, err := annos.Pick(replaced)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, testSiteGrantID("user-1", tc.held), replaced.GetReplacedGrantId())
		})
	}
}

// TestSiteGrant_ServerAdministratorProtected asserts that a site Grant never
// downgrades a ServerAdministrator, and that the refusal is never reported as
// GrantAlreadyExists.
func TestSiteGrant_ServerAdministratorProtected(t *testing.T) {
	t.Parallel()

	for _, target := range []string{viewer, creator, unlicensed, siteAdministratorCreator} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			users := newSiteTestUsers(map[string]string{"user-1": serverAdministrator})
			s := newSiteBuilder(newSiteTestClient(t, users))

			annos, err := s.Grant(context.Background(), testUserPrincipal("user-1"), testSiteEntitlement(target))
			require.Error(t, err)
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
			require.Empty(t, users.recordedPuts(), "a ServerAdministrator must not be written")
			require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
			require.Equal(t, serverAdministrator, users.roles["user-1"])
		})
	}
}

// TestSiteRevoke_AdminRoles asserts that a held SiteAdministrator* role is reset
// to Unlicensed, a held ServerAdministrator is refused without a write, and a
// stale revoke of an admin role is a no-op.
func TestSiteRevoke_AdminRoles(t *testing.T) {
	t.Parallel()

	for _, held := range []string{siteAdministrator, siteAdministratorCreator, siteAdministratorExplorer} {
		t.Run("held "+held, func(t *testing.T) {
			t.Parallel()

			users := newSiteTestUsers(map[string]string{"user-1": held})
			s := newSiteBuilder(newSiteTestClient(t, users))

			annos, err := s.Revoke(context.Background(), testSiteGrant(held, "user-1"))
			require.NoError(t, err)
			require.Equal(t, []recordedPut{{userID: "user-1", siteRole: unlicensed}}, users.recordedPuts())
			require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		})
	}

	t.Run("held ServerAdministrator is protected", func(t *testing.T) {
		t.Parallel()

		users := newSiteTestUsers(map[string]string{"user-1": serverAdministrator})
		s := newSiteBuilder(newSiteTestClient(t, users))

		annos, err := s.Revoke(context.Background(), testSiteGrant(serverAdministrator, "user-1"))
		require.Error(t, err)
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Empty(t, users.recordedPuts())
		require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		require.Equal(t, serverAdministrator, users.roles["user-1"])
	})

	t.Run("stale revoke of a role while ServerAdministrator", func(t *testing.T) {
		t.Parallel()

		users := newSiteTestUsers(map[string]string{"user-1": serverAdministrator})
		s := newSiteBuilder(newSiteTestClient(t, users))

		annos, err := s.Revoke(context.Background(), testSiteGrant(creator, "user-1"))
		require.NoError(t, err)
		require.Empty(t, users.recordedPuts())
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	})

	t.Run("stale revoke of an admin role", func(t *testing.T) {
		t.Parallel()

		users := newSiteTestUsers(map[string]string{"user-1": viewer})
		s := newSiteBuilder(newSiteTestClient(t, users))

		annos, err := s.Revoke(context.Background(), testSiteGrant(siteAdministrator, "user-1"))
		require.NoError(t, err)
		require.Empty(t, users.recordedPuts())
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	})
}

// TestSite_ServerAdministratorChangedBetweenCalls asserts the guard reads fresh
// state: a user promoted to ServerAdministrator out-of-band between two calls on
// one client is protected on the second call.
func TestSite_ServerAdministratorChangedBetweenCalls(t *testing.T) {
	t.Parallel()

	users := newSiteTestUsers(map[string]string{"user-1": viewer})
	s := newSiteBuilder(newSiteTestClient(t, users))
	ctx := context.Background()

	_, err := s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(viewer))
	require.NoError(t, err)

	users.set("user-1", serverAdministrator)

	_, err = s.Grant(ctx, testUserPrincipal("user-1"), testSiteEntitlement(creator))
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Empty(t, users.recordedPuts())
}

// TestSiteEntitlements_ExclusionGroup asserts every Site Role entitlement carries
// an exclusion group scoped to its site: one group within a site, distinct groups
// across sites, with entitlement and grant IDs unchanged.
func TestSiteEntitlements_ExclusionGroup(t *testing.T) {
	t.Parallel()

	s := newSiteBuilder(nil)
	siteB := &v2.Resource{
		Id:          &v2.ResourceId{ResourceType: resourceTypeSite.Id, Resource: "site-2"},
		DisplayName: "Site 2",
	}
	groupOf := func(e *v2.Entitlement) string {
		eg := &v2.EntitlementExclusionGroup{}
		annos := annotations.Annotations(e.GetAnnotations())
		ok, err := annos.Pick(eg)
		require.NoError(t, err)
		require.True(t, ok, "entitlement %s has no exclusion group", e.GetId())
		return eg.GetExclusionGroupId()
	}

	entsA, _, err := s.Entitlements(context.Background(), testSiteResource(), rs.SyncOpAttrs{})
	require.NoError(t, err)
	entsB, _, err := s.Entitlements(context.Background(), siteB, rs.SyncOpAttrs{})
	require.NoError(t, err)
	require.Len(t, entsA, len(roles))
	require.Len(t, entsB, len(roles))

	groupA, groupB := groupOf(entsA[0]), groupOf(entsB[0])
	require.NotEqual(t, groupA, groupB, "different sites must not exclude one another")
	for _, e := range entsA {
		require.Equal(t, groupA, groupOf(e), "roles of one site share a group")
		slug := e.GetSlug()
		require.Equal(t, ent.NewEntitlementID(testSiteResource(), slug), e.GetId(), "entitlement ID must be unchanged")
	}
	for _, e := range entsB {
		require.Equal(t, groupB, groupOf(e))
	}
}
