package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-tableau/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// roleChange describes one license or site-role Grant or Revoke. A license tier
// and a site role are both views of the user's single Tableau site role, so
// both builders write through grantRole and revokeRole.
type roleChange struct {
	client *client.Client
	// label prefixes errors and logs: "license" or "site role".
	label string
	// subject names the tier or role being granted or revoked.
	subject string
	// userID is the user whose site role is read and written.
	userID string
	// holds reports whether a current site role already counts as the
	// entitlement being granted or revoked.
	holds func(siteRole string) bool
	// refuseGrant returns the error that refuses granting over a current site
	// role, or nil when the grant may replace it. Unused by revoke.
	refuseGrant func(siteRole string) error
	// replacedGrantID returns the grant ID that a write displaces when the
	// previous site role is known. Unused by revoke.
	replacedGrantID func(siteRole string) (string, bool)
}

// errSelfLockout refuses a write that would change the role of the user the
// connector signs in as, which could cut the connector off from Tableau.
func (rc *roleChange) errSelfLockout(action string) error {
	return status.Errorf(codes.FailedPrecondition,
		"%s: cannot %s %s for user %s: it is the account this connector signs in as, and changing its role could lock the connector out of Tableau",
		rc.label, action, rc.subject, rc.userID)
}

// isSelf reports whether the change targets the signed-in user.
func (rc *roleChange) isSelf() bool {
	self := rc.client.AuthenticatedUserID()
	return self != "" && self == rc.userID
}

// grantRole sets the user's site role to role after an uncached read:
//   - missing user: NotFound.
//   - holds: no write, GrantAlreadyExists.
//   - the signed-in user, or a role refuseGrant rejects: FailedPrecondition, no write.
//   - otherwise: write, reporting GrantReplaced when the previous role is known.
//
// Rate-limit annotations from the read are merged into every result.
func (rc *roleChange) grantRole(ctx context.Context, role string) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)

	user, getAnnos, err := rc.client.GetUser(ctx, rc.userID)
	if err != nil {
		if client.IsNotFoundError(err) {
			return getAnnos, fmt.Errorf("%s: cannot grant %s: user %s not found: %w", rc.label, rc.subject, rc.userID, err)
		}
		return getAnnos, fmt.Errorf("failed to read user %s for %s grant: %w", rc.userID, rc.label, err)
	}

	if rc.holds(user.SiteRole) {
		log.Debug(rc.label+" grant: user already holds it, no write needed",
			zap.String("user_id", rc.userID), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyExists{})), nil
	}

	if rc.isSelf() {
		return getAnnos, rc.errSelfLockout("grant")
	}

	if refusal := rc.refuseGrant(user.SiteRole); refusal != nil {
		return getAnnos, refusal
	}

	putAnnos, err := rc.client.UpdateUserSiteRole(ctx, rc.userID, role)
	annos := mergeAnnotations(getAnnos, putAnnos)
	if err != nil {
		return annos, fmt.Errorf("failed to grant %s %s to user %s: %w", rc.subject, rc.label, rc.userID, err)
	}

	if replacedID, known := rc.replacedGrantID(user.SiteRole); known {
		annos.Append(&v2.GrantReplaced{ReplacedGrantId: replacedID})
	}

	return annos, nil
}

// revokeRole sets the user to Unlicensed, but only while they still hold the
// revoked entitlement; the read is uncached. A missing user, a user who no
// longer holds it, and an already Unlicensed user are GrantAlreadyRevoked with
// no write, so a revoke never wipes access it did not target. Any held role,
// including an administrator role, is reset, except for the signed-in user
// (FailedPrecondition, no write). Rate-limit annotations from the read are
// merged into every result.
func (rc *roleChange) revokeRole(ctx context.Context) (annotations.Annotations, error) {
	log := ctxzap.Extract(ctx)

	user, getAnnos, err := rc.client.GetUser(ctx, rc.userID)
	if err != nil {
		if client.IsNotFoundError(err) {
			log.Debug(rc.label+" revoke: user not found, nothing to revoke", zap.String("user_id", rc.userID))
			return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
		}
		return getAnnos, fmt.Errorf("failed to read user %s for %s revoke: %w", rc.userID, rc.label, err)
	}

	if !rc.holds(user.SiteRole) {
		log.Debug(rc.label+" revoke: user no longer holds it, nothing to revoke",
			zap.String("user_id", rc.userID), zap.String("site_role", user.SiteRole))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
	}

	if user.SiteRole == unlicensed {
		log.Debug(rc.label+" revoke: user already Unlicensed (floor role), nothing to remove",
			zap.String("user_id", rc.userID))
		return mergeAnnotations(getAnnos, annotations.New(&v2.GrantAlreadyRevoked{})), nil
	}

	if rc.isSelf() {
		return getAnnos, rc.errSelfLockout("revoke")
	}

	putAnnos, err := rc.client.UpdateUserSiteRole(ctx, rc.userID, unlicensed)
	annos := mergeAnnotations(getAnnos, putAnnos)
	if err != nil {
		return annos, fmt.Errorf("failed to revoke %s %s from user %s: %w", rc.subject, rc.label, rc.userID, err)
	}

	return annos, nil
}
