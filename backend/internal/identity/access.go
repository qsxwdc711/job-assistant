package identity

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CheckAccess rechecks and holds current account state until a business transaction completes.
func CheckAccess(ctx context.Context, tx pgx.Tx, actorID string, action Action, ownerID string, now time.Time) (User, error) {
	if actorID == "" {
		return User{}, ErrUnauthenticated
	}
	actor, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 FOR SHARE", actorID))
	if err != nil {
		return User{}, err
	}
	if err := Authorize(actor, action, ownerID, now); err != nil {
		return User{}, err
	}
	return actor, nil
}

// AppendAudit records a business mutation in its owning transaction without private content.
func AppendAudit(ctx context.Context, tx pgx.Tx, actorID, action, target, requestID string, metadata any, now time.Time) error {
	return recordAudit(ctx, tx, &actorID, action, target, requestID, metadata, now)
}
