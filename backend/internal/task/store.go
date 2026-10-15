// Package task provides leased, bounded, persistent private jobs without putting documents in payloads.
package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobassistant/backend/internal/identity"
)

var ErrCancelled = errors.New("task permission, lease or resource changed")

// Job exposes progress but keeps owner, resource identity and lease credentials out of JSON.
type Job struct {
	ID           string    `json:"id"`
	OwnerID      string    `json:"-"`
	ActorID      string    `json:"-"`
	AuthVersion  int64     `json:"-"`
	OwnerVersion int64     `json:"-"`
	Kind         string    `json:"kind"`
	ResourceID   string    `json:"-"`
	Revision     int64     `json:"-"`
	State        string    `json:"state"`
	Attempts     int       `json:"attempts"`
	MaxAttempts  int       `json:"-"`
	LeaseToken   string    `json:"-"`
	ErrorCode    string    `json:"error_code"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Store owns the persistent queue, not worker goroutines.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `id::text,owner_user_id::text,actor_user_id::text,auth_version,owner_auth_version,kind,resource_id::text,resource_revision,state,attempts,max_attempts,lease_token,error_code,created_at,updated_at`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.OwnerID, &j.ActorID, &j.AuthVersion, &j.OwnerVersion, &j.Kind, &j.ResourceID, &j.Revision, &j.State, &j.Attempts, &j.MaxAttempts, &j.LeaseToken, &j.ErrorCode, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, identity.ErrNotFound
	}
	return j, err
}

// Enqueue participates in the caller's already-authorized business transaction.
func Enqueue(ctx context.Context, tx pgx.Tx, actor, owner identity.User, kind, resource string, revision int64, now time.Time) (Job, error) {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM private_tasks WHERE owner_user_id=$1 AND state IN ('queued','running')`, owner.ID).Scan(&count); err != nil {
		return Job{}, err
	}
	if count >= 200 {
		return Job{}, fmt.Errorf("%w: too many pending tasks", identity.ErrInvalidInput)
	}
	return scan(tx.QueryRow(ctx, `INSERT INTO private_tasks(owner_user_id,actor_user_id,auth_version,owner_auth_version,kind,resource_id,resource_revision,available_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$8) RETURNING `+columns, owner.ID, actor.ID, actor.AuthVersion, owner.AuthVersion, kind, resource, revision, now))
}

// CheckAccess rechecks both initiating privileges and the owner's current account version.
func CheckAccess(ctx context.Context, tx pgx.Tx, j Job, action identity.Action, now time.Time) error {
	actor, err := identity.CheckAccess(ctx, tx, j.ActorID, action, j.OwnerID, now)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrNotFound) {
		return ErrCancelled
	}
	if err != nil {
		return err
	}
	owner, err := identity.CheckAccess(ctx, tx, j.OwnerID, identity.ManageApplications, "", now)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrNotFound) {
		return ErrCancelled
	}
	if err != nil {
		return err
	}
	if actor.AuthVersion != j.AuthVersion || owner.AuthVersion != j.OwnerVersion {
		return ErrCancelled
	}
	return nil
}

// CheckLease is called after account/resource locks, immediately before committing private output.
func CheckLease(ctx context.Context, tx pgx.Tx, j Job, now time.Time) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT state='running' AND lease_token=$2 AND lease_until>$3 FROM private_tasks WHERE id=$1 FOR SHARE`, j.ID, j.LeaseToken, now).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return ErrCancelled
	}
	return err
}

// Claim atomically leases one ready job; an expired final attempt is claimed once for terminal cleanup.
func (s *Store) Claim(ctx context.Context, now time.Time) (Job, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Job{}, err
	}
	return scan(s.pool.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM private_tasks WHERE available_at<=$1 AND (state='queued' OR (state='running' AND lease_until<=$1))
 ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE private_tasks t SET state='running',attempts=attempts+1,lease_token=$2,lease_until=$1+interval '120 seconds',updated_at=$1
FROM candidate c WHERE t.id=c.id RETURNING t.`+stringsColumns(), now, hex.EncodeToString(raw[:])))
}

func stringsColumns() string {
	return `id::text,t.owner_user_id::text,t.actor_user_id::text,t.auth_version,t.owner_auth_version,t.kind,t.resource_id::text,t.resource_revision,t.state,t.attempts,t.max_attempts,t.lease_token,t.error_code,t.created_at,t.updated_at`
}

// Finish never lets an expired lease change a job now owned by a different worker.
func (s *Store) Finish(ctx context.Context, j Job, code string, retry, cancelled bool, now time.Time) error {
	state := "succeeded"
	if code != "" {
		state = "failed"
	}
	if cancelled {
		state = "cancelled"
	}
	if retry && j.Attempts < j.MaxAttempts {
		state = "queued"
	}
	_, err := s.pool.Exec(ctx, `UPDATE private_tasks SET state=$3,error_code=$4,lease_until=NULL,lease_token='',available_at=$5,updated_at=$6 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>$6`, j.ID, j.LeaseToken, state, code, now.Add(time.Duration(j.Attempts*j.Attempts)*time.Second), now)
	return err
}

// Get exposes task status only to its owner or a current administrator.
func (s *Store) Get(ctx context.Context, actorID, id string, now time.Time) (Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer Rollback(tx)
	actor, err := identity.CheckAccess(ctx, tx, actorID, identity.ManageApplications, "", now)
	if err != nil {
		return Job{}, err
	}
	j, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM private_tasks WHERE id=$1 AND (owner_user_id=$2 OR $3)`, id, actorID, actor.EffectiveRole(now) == identity.RoleAdmin))
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Rollback bounds cleanup independently of a cancelled request context.
func Rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("private transaction rollback failed")
	}
}
