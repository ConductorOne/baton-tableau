package client

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testApp = ConnectedApp{
	ClientID:    "11111111-2222-3333-4444-555555555555",
	SecretID:    "66666666-7777-8888-9999-000000000000",
	SecretValue: "s3cr3t-value",
	Username:    "svc.tableau@example.com",
}

func decodeSegment(t *testing.T, segment string) map[string]any {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	require.NoError(t, err, "segment must be unpadded base64url")

	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

// TestNewConnectedAppJWT_Structure asserts the header and claims Tableau
// requires for direct trust. Tableau rejects the assertion outright if the
// issuer or key identifier are missing from the header, so both are checked
// there rather than only in the claim set.
func TestNewConnectedAppJWT_Structure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	token, err := newConnectedAppJWT(testApp, now)
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "a JWT has three segments")

	header := decodeSegment(t, parts[0])
	require.Equal(t, "HS256", header["alg"])
	require.Equal(t, "JWT", header["typ"])
	require.Equal(t, testApp.SecretID, header["kid"])
	require.Equal(t, testApp.ClientID, header["iss"])

	claims := decodeSegment(t, parts[1])
	require.Equal(t, testApp.ClientID, claims["iss"])
	require.Equal(t, testApp.Username, claims["sub"])
	require.Equal(t, "tableau", claims["aud"])
	require.NotEmpty(t, claims["jti"])

	exp, ok := claims["exp"].(float64)
	require.True(t, ok, "exp must be a numeric date")
	require.Equal(t, now.Add(jwtLifetime).Unix(), int64(exp))
	require.LessOrEqual(t, int64(exp)-now.Unix(), int64(10*time.Minute/time.Second),
		"Tableau rejects assertions valid for more than ten minutes")

	scopes, ok := claims["scp"].([]any)
	require.True(t, ok, "scp must be a list")
	require.Len(t, scopes, len(connectedAppScopes))
	require.Contains(t, scopes, "tableau:users:*")
	require.Contains(t, scopes, "tableau:groups:*")
	// Grant sync reads project, workbook, and view ACLs unconditionally, and
	// Tableau gates those reads behind their own scope. Its absence would not
	// show up until a sync against a site with content.
	require.Contains(t, scopes, "tableau:permissions:read")

	// The connector reads projects and workbooks and edits their permissions,
	// but never writes the objects themselves. Requesting these wildcards would
	// hand a leaked session project deletion and workbook publishing.
	require.NotContains(t, scopes, "tableau:projects:*")
	require.NotContains(t, scopes, "tableau:workbooks:*")
}

// TestNewConnectedAppJWT_Signature recomputes the HMAC over the signing input
// to prove the signature covers exactly the two encoded segments.
func TestNewConnectedAppJWT_Signature(t *testing.T) {
	t.Parallel()

	token, err := newConnectedAppJWT(testApp, time.Now())
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	mac := hmac.New(sha256.New, []byte(testApp.SecretValue))
	_, err = mac.Write([]byte(parts[0] + "." + parts[1]))
	require.NoError(t, err)

	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	require.Equal(t, want, parts[2])
}

// TestNewConnectedAppJWT_UniqueID guards the jti claim. Tableau rejects a
// replayed identifier, so two assertions from the same app must differ.
func TestNewConnectedAppJWT_UniqueID(t *testing.T) {
	t.Parallel()

	now := time.Now()

	first, err := newConnectedAppJWT(testApp, now)
	require.NoError(t, err)

	second, err := newConnectedAppJWT(testApp, now)
	require.NoError(t, err)

	firstID := decodeSegment(t, strings.Split(first, ".")[1])["jti"]
	secondID := decodeSegment(t, strings.Split(second, ".")[1])["jti"]

	require.NotEqual(t, firstID, secondID)
}
