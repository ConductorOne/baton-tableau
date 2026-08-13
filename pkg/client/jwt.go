package client

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Tableau connected apps sign their JWTs with HS256 and nothing else, and the
// signature covers only the two base64url segments. Reaching for a JWT library
// to emit thirty bytes of HMAC would pull a dependency into the module graph
// for a code path that never verifies a token, so the encoder lives here.

// jwtLifetime is how long a generated assertion stays valid. Tableau rejects
// anything beyond ten minutes; the remainder is headroom for clock skew
// between this connector and Tableau Cloud.
const jwtLifetime = 5 * time.Minute

// connectedAppScopes are the access scopes requested when signing in with a
// connected app. Tableau bounds the resulting session by the intersection of
// this list and the scopes enabled on the connected app itself, so the list
// must cover every endpoint this package calls: sites and content reads, the
// full user and group lifecycle for provisioning, and reading and writing
// permissions on projects, workbooks, and views.
//
// Permission reads need their own scope. Grant sync enumerates project,
// workbook, and view ACLs on every run, and Tableau gates those GETs behind
// tableau:permissions:read rather than tableau:content:read — omit it and
// sign-in succeeds while the first site with content fails mid-sync.
//
// Tableau publishes no scope covering /site-auth-configurations, so IDP
// discovery may be refused under a connected app where it succeeds under a
// personal access token. The account-creation path treats that refusal as
// discovery being unavailable and falls back to the site default.
var connectedAppScopes = []string{
	"tableau:content:read",
	"tableau:sites:read",
	"tableau:users:*",
	"tableau:groups:*",
	"tableau:projects:*",
	"tableau:workbooks:*",
	"tableau:permissions:read",
	"tableau:permissions:update",
	"tableau:permissions:delete",
}

// ConnectedApp holds the direct trust credentials for a Tableau connected app.
// Username is the email address of the Tableau user the connector acts as; it
// needs the same site administrator rights a personal access token owner does.
type ConnectedApp struct {
	ClientID    string
	SecretID    string
	SecretValue string
	Username    string
}

// newConnectedAppJWT signs a direct trust assertion for the given connected
// app. Tableau expects the issuer and secret identifier in the header rather
// than the claim set, and repeats the issuer as a claim.
func newConnectedAppJWT(app ConnectedApp, now time.Time) (string, error) {
	header := map[string]any{
		"alg": "HS256",
		"typ": "JWT",
		"kid": app.SecretID,
		"iss": app.ClientID,
	}

	jti, err := newJWTID()
	if err != nil {
		return "", err
	}

	claims := map[string]any{
		"iss": app.ClientID,
		"sub": app.Username,
		"aud": "tableau",
		"jti": jti,
		"exp": now.Add(jwtLifetime).Unix(),
		"scp": connectedAppScopes,
	}

	headerSegment, err := encodeJWTSegment(header)
	if err != nil {
		return "", fmt.Errorf("failed to encode JWT header: %w", err)
	}

	claimsSegment, err := encodeJWTSegment(claims)
	if err != nil {
		return "", fmt.Errorf("failed to encode JWT claims: %w", err)
	}

	signingInput := headerSegment + "." + claimsSegment

	mac := hmac.New(sha256.New, []byte(app.SecretValue))
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}

// newJWTID returns a unique value for the jti claim. Tableau rejects replayed
// identifiers, so this must differ on every sign-in.
func newJWTID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate JWT ID: %w", err)
	}

	return hex.EncodeToString(buf), nil
}

func encodeJWTSegment(v any) (string, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
