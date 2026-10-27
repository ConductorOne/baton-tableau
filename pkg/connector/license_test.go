package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestLicenseResource_Trait asserts the LicenseProfileTrait is attached with the
// right name, entitlement ID, and seat counts. The baton CLI does not decode the
// trait body, so this direct assertion is the reliable verification path.
func TestLicenseResource_Trait(t *testing.T) {
	t.Parallel()

	creatorCap := "5"
	zeroCap := "0"

	tests := []struct {
		name          string
		license       string
		purchased     *string
		consumed      int64
		wantName      string
		wantEntID     string
		wantPurchased int64
		wantConsumed  int64
	}{
		{
			name:          "capped tier reports seats",
			license:       creator,
			purchased:     &creatorCap,
			consumed:      2,
			wantName:      "Creator",
			wantEntID:     "license:creator:member",
			wantPurchased: 5,
			wantConsumed:  2,
		},
		{
			name:      "floor tier without capacity omits seats",
			license:   unlicensed,
			purchased: nil,
			consumed:  0,
			wantName:  "Unlicensed",
			wantEntID: "license:unlicensed:member",
			// no capacity -> WithLicenseSeats not called -> both zero
			wantPurchased: 0,
			wantConsumed:  0,
		},
		{
			name:      "zero capacity omits seats",
			license:   viewer,
			purchased: &zeroCap,
			consumed:  3,
			wantName:  "Viewer",
			wantEntID: "license:viewer:member",
			// "0" capacity -> WithLicenseSeats not called (no "0 of N")
			wantPurchased: 0,
			wantConsumed:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := licenseResource(tt.license, tt.purchased, tt.consumed)
			require.NoError(t, err)

			profile, err := resource.GetLicenseProfileTrait(res)
			require.NoError(t, err, "license resource must carry a LicenseProfileTrait")

			require.Equal(t, tt.wantName, profile.GetLicenseName())
			require.Equal(t, []string{tt.wantEntID}, profile.GetEntitlementIds())
			require.Equal(t, tt.wantPurchased, profile.GetPurchasedSeats())
			require.Equal(t, tt.wantConsumed, profile.GetConsumedSeats())
		})
	}
}

// TestCapacitySeats locks in the parse + positive-guard contract: only a
// present, numeric, positive capacity string yields seats.
func TestCapacitySeats(t *testing.T) {
	t.Parallel()

	str := func(s string) *string { return &s }
	tests := []struct {
		name   string
		in     *string
		wantN  int64
		wantOK bool
	}{
		{name: "nil", in: nil, wantN: 0, wantOK: false},
		{name: "positive", in: str("25"), wantN: 25, wantOK: true},
		{name: "zero", in: str("0"), wantN: 0, wantOK: false},
		{name: "whitespace-padded", in: str("  7 "), wantN: 7, wantOK: true},
		{name: "negative", in: str("-3"), wantN: 0, wantOK: false},
		{name: "non-numeric", in: str("unlimited"), wantN: 0, wantOK: false},
		{name: "empty", in: str(""), wantN: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n, ok := capacitySeats(tt.in)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantN, n)
		})
	}
}

// =============================================================================
// Grant / Revoke
// =============================================================================

const (
	licenseTestSiteID = "site-1"

	// licenseTestServerError is a pseudo role that makes the mock GetUser return HTTP 500.
	licenseTestServerError = "__server_error__"

	licenseTestRateLimit     = 100
	licenseTestRateRemaining = 42
)

// licenseTestUsersMu guards the users maps handed to newLicenseTestClient: the
// mock server reads and applies PUTs to them from its own goroutine while a
// test may change a role between calls.
var licenseTestUsersMu sync.Mutex

type recordedPut struct {
	userID   string
	siteRole string
}

// newLicenseTestClient starts a mock Tableau API server that authenticates
// any login, serves GetUser from users (keyed by user ID, valued by current
// site role, updated by every PUT; a missing key means a 404 and the licenseTestServerError role
// means a 500), stamps rate-limit headers on every GET and PUT so tests can check the
// annotations are propagated, and records every PUT
// (UpdateUserSiteRole) it receives so tests can assert exact write counts
// and bodies.
func newLicenseTestClient(t *testing.T, users map[string]string, puts *[]recordedPut) *client.Client {
	t.Helper()

	usersPathPrefix := "/sites/" + licenseTestSiteID + "/users/"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/signin":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"credentials": map[string]any{
					"site":  map[string]any{"id": licenseTestSiteID},
					"user":  map[string]any{"id": "me"},
					"token": "test-token",
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, usersPathPrefix):
			userID := strings.TrimPrefix(r.URL.Path, usersPathPrefix)
			licenseTestUsersMu.Lock()
			role, ok := users[userID]
			licenseTestUsersMu.Unlock()
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Ratelimit-Limit", strconv.Itoa(licenseTestRateLimit))
				w.Header().Set("X-Ratelimit-Remaining", strconv.Itoa(licenseTestRateRemaining))
				w.Header().Set("X-Ratelimit-Reset", "30")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"summary":"Not Found","detail":"user not found","code":"0x1"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Ratelimit-Limit", strconv.Itoa(licenseTestRateLimit))
			w.Header().Set("X-Ratelimit-Remaining", strconv.Itoa(licenseTestRateRemaining))
			w.Header().Set("X-Ratelimit-Reset", "30")
			if role == licenseTestServerError {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"summary":"Internal Server Error","detail":"boom","code":"0x3"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user": map[string]any{"id": userID, "siteRole": role},
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, usersPathPrefix):
			userID := strings.TrimPrefix(r.URL.Path, usersPathPrefix)
			var body struct {
				User struct {
					SiteRole string `json:"siteRole"`
				} `json:"user"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			licenseTestUsersMu.Lock()
			*puts = append(*puts, recordedPut{userID: userID, siteRole: body.User.SiteRole})
			users[userID] = body.User.SiteRole
			licenseTestUsersMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Ratelimit-Limit", strconv.Itoa(licenseTestRateLimit))
			w.Header().Set("X-Ratelimit-Remaining", strconv.Itoa(licenseTestRateRemaining))
			w.Header().Set("X-Ratelimit-Reset", "30")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user": map[string]any{"id": userID, "siteRole": body.User.SiteRole},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"summary":"Not Found","detail":"unexpected request","code":"0x2"}}`))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := client.New(context.Background(), "unused", licenseTestSiteID, "token-name", "token-secret", "3.19", srv.URL)
	require.NoError(t, err)
	return c
}

func testUserPrincipal(id string) *v2.Resource {
	return &v2.Resource{Id: &v2.ResourceId{ResourceType: resourceTypeUser.Id, Resource: id}}
}

func testLicenseEntitlement(tier string) *v2.Entitlement {
	return &v2.Entitlement{
		Resource: &v2.Resource{
			Id: &v2.ResourceId{ResourceType: resourceTypeLicense.Id, Resource: strings.ToLower(tier)},
		},
	}
}

// TestLicenseGrant_AlreadyInTier asserts that granting a tier to a user whose
// current site role already satisfies that tier performs no write and reports
// GrantAlreadyExists.
func TestLicenseGrant_AlreadyInTier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tier    string
		curRole string
	}{
		{"creator tier: Creator", creator, creator},
		{"creator tier: SiteAdministratorCreator", creator, siteAdministratorCreator},
		{"creator tier: ServerAdministrator", creator, serverAdministrator},
		{"explorer tier: Explorer", explorer, explorer},
		{"explorer tier: SiteAdministratorExplorer", explorer, siteAdministratorExplorer},
		{"explorer tier: ExplorerCanPublish", explorer, explorerCanPublish},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var puts []recordedPut
			c := newLicenseTestClient(t, map[string]string{"user-1": tt.curRole}, &puts)
			l := newLicenseBuilder(c)

			annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(tt.tier))
			require.NoError(t, err)
			require.Empty(t, puts, "already-in-tier grant must not write")
			require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		})
	}
}

// TestLicenseGrant_CrossTierNonAdmin asserts that a non-admin role outside the
// target tier is replaced, with exactly one PUT and a GrantReplaced annotation
// naming the user's previous license grant.
func TestLicenseGrant_CrossTierNonAdmin(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": viewer}, &puts)
	l := newLicenseBuilder(c)

	annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)

	require.Len(t, puts, 1, "cross-tier grant must write exactly once")
	require.Equal(t, "user-1", puts[0].userID)
	require.Equal(t, creator, puts[0].siteRole)

	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))

	var replaced v2.GrantReplaced
	found, err := annos.Pick(&replaced)
	require.NoError(t, err)
	require.True(t, found, "expected a GrantReplaced annotation")
	require.Equal(t, "license:viewer:member:user:user-1", replaced.GetReplacedGrantId())
}

// TestLicenseGrant_CrossTierAdmin asserts that granting a different tier to a
// user currently holding an admin role is refused with FailedPrecondition
// rather than silently stripping admin rights, and does not write.
func TestLicenseGrant_CrossTierAdmin(t *testing.T) {
	t.Parallel()

	adminRolesUnderTest := []string{siteAdministrator, siteAdministratorCreator, siteAdministratorExplorer, serverAdministrator}

	for _, role := range adminRolesUnderTest {
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			var puts []recordedPut
			c := newLicenseTestClient(t, map[string]string{"user-1": role}, &puts)
			l := newLicenseBuilder(c)

			// Pick a target tier the role does not already satisfy.
			target := explorer
			if role == siteAdministratorExplorer || role == siteAdministrator {
				target = creator
			}

			_, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(target))
			require.Error(t, err)
			require.Equal(t, codes.FailedPrecondition, status.Code(err))
			require.Empty(t, puts, "refused admin cross-tier grant must not write")
		})
	}
}

// TestLicenseGrant_UnknownUser asserts that a 404 on the single-user GET means
// there is nothing to grant to.
func TestLicenseGrant_UnknownUser(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{}, &puts)
	l := newLicenseBuilder(c)

	_, err := l.Grant(context.Background(), testUserPrincipal("ghost"), testLicenseEntitlement(creator))
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, puts)
}

// TestLicenseRevoke_Stale asserts that revoking a tier the user no longer
// holds (e.g. a later Grant already moved them elsewhere) does not write and
// reports GrantAlreadyRevoked.
func TestLicenseRevoke_Stale(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts)
	l := newLicenseBuilder(c)

	gr := &v2.Grant{
		Entitlement: testLicenseEntitlement(viewer),
		Principal:   testUserPrincipal("user-1"),
	}

	annos, err := l.Revoke(context.Background(), gr)
	require.NoError(t, err)
	require.Empty(t, puts, "stale revoke must not write")
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
}

// TestLicenseRevoke_HoldsTier asserts that a user who still holds the tier is
// unlicensed via exactly one PUT.
func TestLicenseRevoke_HoldsTier(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts)
	l := newLicenseBuilder(c)

	gr := &v2.Grant{
		Entitlement: testLicenseEntitlement(creator),
		Principal:   testUserPrincipal("user-1"),
	}

	_, err := l.Revoke(context.Background(), gr)
	require.NoError(t, err)
	require.Len(t, puts, 1)
	require.Equal(t, "user-1", puts[0].userID)
	require.Equal(t, unlicensed, puts[0].siteRole)
}

// TestLicenseRevoke_UserNotFound asserts that a deleted user (404) has nothing
// left to revoke.
func TestLicenseRevoke_UserNotFound(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{}, &puts)
	l := newLicenseBuilder(c)

	gr := &v2.Grant{
		Entitlement: testLicenseEntitlement(creator),
		Principal:   testUserPrincipal("ghost"),
	}

	annos, err := l.Revoke(context.Background(), gr)
	require.NoError(t, err)
	require.Empty(t, puts)
	require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
}

// requireSingleRateLimit asserts annos carries exactly one RateLimitDescription
// and that it is the one the mock GET responses stamped.
func requireSingleRateLimit(t *testing.T, annos annotations.Annotations) {
	t.Helper()

	count := 0
	for _, a := range annos {
		if a.MessageIs(&v2.RateLimitDescription{}) {
			count++
		}
	}
	require.Equal(t, 1, count, "expected exactly one RateLimitDescription")

	var rl v2.RateLimitDescription
	found, err := annos.Pick(&rl)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, licenseTestRateLimit, rl.GetLimit())
	require.EqualValues(t, licenseTestRateRemaining, rl.GetRemaining())
}

// TestLicense_GetUserServerError asserts that a non-404 failure reading the
// user surfaces as an error (not NotFound) and never falls through to a write.
func TestLicense_GetUserServerError(t *testing.T) {
	t.Parallel()

	t.Run("grant", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		c := newLicenseTestClient(t, map[string]string{"user-1": licenseTestServerError}, &puts)
		l := newLicenseBuilder(c)

		_, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
		require.Error(t, err)
		require.NotEqual(t, codes.NotFound, status.Code(err))
		require.Empty(t, puts, "failed read must not write")
	})

	t.Run("revoke", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		c := newLicenseTestClient(t, map[string]string{"user-1": licenseTestServerError}, &puts)
		l := newLicenseBuilder(c)

		annos, err := l.Revoke(context.Background(), &v2.Grant{
			Entitlement: testLicenseEntitlement(creator),
			Principal:   testUserPrincipal("user-1"),
		})
		require.Error(t, err)
		require.NotEqual(t, codes.NotFound, status.Code(err))
		require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}), "a server error must not be reported as already revoked")
		require.Empty(t, puts, "failed read must not write")
	})
}

// TestLicenseGrant_RoleOutsideEveryTier asserts that a current role belonging
// to no tier (e.g. Guest) is overwritten with one PUT and, since there is no
// previous license grant to name, no GrantReplaced.
func TestLicenseGrant_RoleOutsideEveryTier(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": "Guest"}, &puts)
	l := newLicenseBuilder(c)

	annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: creator}}, puts)
	require.False(t, annos.Contains(&v2.GrantReplaced{}))
	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}))
}

// TestLicenseGrant_ExplorerCanPublishToCreator asserts that ExplorerCanPublish
// counts as the Explorer tier: granting Creator writes Creator once and
// reports the Explorer license grant as replaced.
func TestLicenseGrant_ExplorerCanPublishToCreator(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": explorerCanPublish}, &puts)
	l := newLicenseBuilder(c)

	annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)
	require.Equal(t, []recordedPut{{userID: "user-1", siteRole: creator}}, puts)

	var replaced v2.GrantReplaced
	found, err := annos.Pick(&replaced)
	require.NoError(t, err)
	require.True(t, found, "expected a GrantReplaced annotation")
	require.Equal(t, "license:explorer:member:user:user-1", replaced.GetReplacedGrantId())
}

// TestLicense_PropagatesGetUserAnnotations asserts that the rate-limit
// annotations from the initial GetUser read are present on every result,
// merged without duplicating the PUT's own RateLimitDescription.
func TestLicense_PropagatesGetUserAnnotations(t *testing.T) {
	t.Parallel()

	revokeOf := func(tier string) *v2.Grant {
		return &v2.Grant{Entitlement: testLicenseEntitlement(tier), Principal: testUserPrincipal("user-1")}
	}

	t.Run("grant already exists", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts))
		annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
		requireSingleRateLimit(t, annos)
	})

	t.Run("grant refused for admin role", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": siteAdministratorCreator}, &puts))
		annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(explorer))
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		requireSingleRateLimit(t, annos)
	})

	t.Run("grant user not found", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{}, &puts))
		annos, err := l.Grant(context.Background(), testUserPrincipal("ghost"), testLicenseEntitlement(creator))
		require.Equal(t, codes.NotFound, status.Code(err))
		requireSingleRateLimit(t, annos)
	})

	t.Run("grant write path", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": viewer}, &puts))
		annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantReplaced{}))
		requireSingleRateLimit(t, annos)
	})

	t.Run("revoke already revoked", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts))
		annos, err := l.Revoke(context.Background(), revokeOf(viewer))
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		requireSingleRateLimit(t, annos)
	})

	t.Run("revoke user not found", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{}, &puts))
		annos, err := l.Revoke(context.Background(), &v2.Grant{Entitlement: testLicenseEntitlement(creator), Principal: testUserPrincipal("ghost")})
		require.NoError(t, err)
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
		requireSingleRateLimit(t, annos)
	})

	t.Run("revoke write path", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts))
		annos, err := l.Revoke(context.Background(), revokeOf(creator))
		require.NoError(t, err)
		require.Len(t, puts, 1)
		requireSingleRateLimit(t, annos)
	})
}

// TestLicenseRevoke_UnlicensedTier asserts that Unlicensed is the floor tier:
// revoking it from an Unlicensed user is rejected without a write, while
// revoking it from a user who holds some other tier is a stale revoke.
func TestLicenseRevoke_UnlicensedTier(t *testing.T) {
	t.Parallel()

	t.Run("user is unlicensed", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": unlicensed}, &puts))

		annos, err := l.Revoke(context.Background(), &v2.Grant{
			Entitlement: testLicenseEntitlement(unlicensed),
			Principal:   testUserPrincipal("user-1"),
		})
		require.Error(t, err)
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Contains(t, err.Error(), "Unlicensed is the floor tier")
		require.Empty(t, puts, "rejected revoke must not write")
		requireSingleRateLimit(t, annos)
	})

	t.Run("user holds another tier", func(t *testing.T) {
		t.Parallel()

		var puts []recordedPut
		l := newLicenseBuilder(newLicenseTestClient(t, map[string]string{"user-1": creator}, &puts))

		annos, err := l.Revoke(context.Background(), &v2.Grant{
			Entitlement: testLicenseEntitlement(unlicensed),
			Principal:   testUserPrincipal("user-1"),
		})
		require.NoError(t, err)
		require.Empty(t, puts)
		require.True(t, annos.Contains(&v2.GrantAlreadyRevoked{}))
	})
}

// TestLicense_GrantThenRevokeSameClient asserts that a revoke following a
// grant on one client reads the role the grant wrote rather than a cached
// pre-grant response.
func TestLicense_GrantThenRevokeSameClient(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	c := newLicenseTestClient(t, map[string]string{"user-1": viewer}, &puts)
	l := newLicenseBuilder(c)

	_, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)
	require.Len(t, puts, 1)
	require.Equal(t, creator, puts[0].siteRole)

	annos, err := l.Revoke(context.Background(), &v2.Grant{
		Entitlement: testLicenseEntitlement(creator),
		Principal:   testUserPrincipal("user-1"),
	})
	require.NoError(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyRevoked{}), "revoke must see the granted role")
	require.Len(t, puts, 2)
	require.Equal(t, unlicensed, puts[1].siteRole)
}

// TestLicenseGrant_RoleChangedBetweenGrants asserts that a second grant on the
// same client sees a role changed outside the connector after the first read.
func TestLicenseGrant_RoleChangedBetweenGrants(t *testing.T) {
	t.Parallel()

	var puts []recordedPut
	users := map[string]string{"user-1": siteAdministratorCreator}
	c := newLicenseTestClient(t, users, &puts)
	l := newLicenseBuilder(c)

	annos, err := l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)
	require.True(t, annos.Contains(&v2.GrantAlreadyExists{}))
	require.Empty(t, puts)

	licenseTestUsersMu.Lock()
	users["user-1"] = viewer
	licenseTestUsersMu.Unlock()

	annos, err = l.Grant(context.Background(), testUserPrincipal("user-1"), testLicenseEntitlement(creator))
	require.NoError(t, err)
	require.False(t, annos.Contains(&v2.GrantAlreadyExists{}), "grant must see the externally changed role")
	require.Len(t, puts, 1)
	require.Equal(t, creator, puts[0].siteRole)
}
