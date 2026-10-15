package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
)

// Store owns private record transactions and never trusts a client-supplied owner.
type Store struct{ pool *pgxpool.Pool }

// NewStore creates private progress storage for an initialized database.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("application transaction rollback failed")
	}
}

func (s *Store) authorized(ctx context.Context, scope Scope, now time.Time) (pgx.Tx, identity.User, error) {
	if scope.ActorID == "" {
		return nil, identity.User{}, identity.ErrUnauthenticated
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, identity.User{}, err
	}
	action := identity.ManageApplications
	if scope.RequireAdmin {
		action = identity.ManageUsers
	}
	actor, err := identity.CheckAccess(ctx, tx, scope.ActorID, action, "", now)
	if err == nil && scope.OwnerID != "" {
		err = identity.Authorize(actor, identity.ManageApplications, scope.OwnerID, now)
	}
	if err != nil {
		rollback(tx)
		return nil, identity.User{}, err
	}
	return tx, actor, nil
}

func ownerFor(scope Scope) string {
	if scope.OwnerID != "" {
		return scope.OwnerID
	}
	if scope.RequireAdmin {
		return ""
	}
	return scope.ActorID
}

func validate(input Input) error {
	if !slices.Contains([]string{"draft", "submitted", "written_test", "interview", "offer", "rejected", "withdrawn"}, input.Status) || utf8.RuneCountInString(input.Notes) > 4000 || utf8.RuneCountInString(input.NextAction) > 200 {
		return identity.ErrInvalidInput
	}
	return nil
}

type scanner interface{ Scan(...any) error }

const recordColumns = `a.id::text,a.owner_user_id::text,u.username,a.job_id::text,a.status,a.notes,a.next_action,a.next_action_at,a.version,a.created_at,a.updated_at,a.resume_version_id::text`

func scanRecord(row scanner) (Record, error) {
	var record Record
	err := row.Scan(&record.ID, &record.OwnerUserID, &record.OwnerUsername, &record.JobID, &record.Status, &record.Notes, &record.NextAction, &record.NextActionAt, &record.Version, &record.CreatedAt, &record.UpdatedAt, &record.ResumeVersionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, identity.ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read application: %w", err)
	}
	return record, nil
}

func readRecord(ctx context.Context, tx pgx.Tx, scope Scope, actor identity.User, id string, lock bool, now time.Time) (Record, error) {
	query := "SELECT " + recordColumns + " FROM applications a JOIN users u ON u.id=a.owner_user_id WHERE a.id::text=$1 AND (a.owner_user_id::text=$2 OR $3)"
	if scope.OwnerID != "" {
		query += " AND a.owner_user_id::text=$4"
	}
	if lock {
		query += " FOR UPDATE OF a"
	}
	args := []any{id, actor.ID, actor.EffectiveRole(now) == identity.RoleAdmin}
	if scope.OwnerID != "" {
		args = append(args, scope.OwnerID)
	}
	return scanRecord(tx.QueryRow(ctx, query, args...))
}

func attachJobs(ctx context.Context, tx pgx.Tx, records []Record, now time.Time) error {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.JobID)
	}
	jobs, err := catalog.LoadJobs(ctx, tx, ids, now)
	if err != nil {
		return err
	}
	for i := range records {
		job, ok := jobs[records[i].JobID]
		if !ok {
			return identity.ErrNotFound
		}
		records[i].Job = job
	}
	return nil
}

// List returns only the authorized owner or a current administrator's explicit scope.
func (s *Store) List(ctx context.Context, scope Scope, limit, offset int, now time.Time) (Page, error) {
	page := Page{Items: []Record{}, Limit: limit, Offset: offset}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return page, identity.ErrInvalidInput
	}
	tx, _, err := s.authorized(ctx, scope, now)
	if err != nil {
		return page, err
	}
	defer rollback(tx)
	owner := ownerFor(scope)
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM applications WHERE ($1='' OR owner_user_id::text=$1)", owner).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, "SELECT "+recordColumns+" FROM applications a JOIN users u ON u.id=a.owner_user_id WHERE ($1='' OR a.owner_user_id::text=$1) ORDER BY a.updated_at DESC,a.id LIMIT $2 OFFSET $3", owner, limit, offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Items = append(page.Items, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if err := attachJobs(ctx, tx, page.Items, now); err != nil {
		return page, err
	}
	if err := tx.Commit(ctx); err != nil {
		return page, err
	}
	return page, nil
}

// Get returns one authorized record, retaining the referenced job after closure or hiding.
func (s *Store) Get(ctx context.Context, scope Scope, id string, now time.Time) (Record, error) {
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return Record{}, err
	}
	defer rollback(tx)
	record, err := readRecord(ctx, tx, scope, actor, id, false, now)
	if err != nil {
		return Record{}, err
	}
	items := []Record{record}
	if err := attachJobs(ctx, tx, items, now); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return items[0], nil
}

// Events returns private revisions after the same ownership checks as the main record.
func (s *Store) Events(ctx context.Context, scope Scope, id string, now time.Time) ([]Event, error) {
	tx, actor, err := s.authorized(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if _, err := readRecord(ctx, tx, scope, actor, id, false, now); err != nil {
		return nil, err
	}
	items := []Event{}
	rows, err := tx.Query(ctx, "SELECT id::text,actor_user_id::text,status,notes,next_action,next_action_at,version,created_at FROM application_events WHERE application_id::text=$1 ORDER BY version DESC LIMIT 100", id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ActorUserID, &e.Status, &e.Notes, &e.NextAction, &e.NextActionAt, &e.Version, &e.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func recordEvent(ctx context.Context, tx pgx.Tx, actorID string, record Record) error {
	_, err := tx.Exec(ctx, "INSERT INTO application_events(application_id,actor_user_id,status,notes,next_action,next_action_at,version,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", record.ID, actorID, record.Status, record.Notes, record.NextAction, record.NextActionAt, record.Version, record.UpdatedAt)
	return err
}

func auditAdmin(ctx context.Context, tx pgx.Tx, actor identity.User, record Record, action, requestID string, now time.Time) error {
	if actor.EffectiveRole(now) != identity.RoleAdmin {
		return nil
	}
	return identity.AppendAudit(ctx, tx, actor.ID, action, record.ID, requestID, map[string]any{"owner_user_id": record.OwnerUserID, "status": record.Status, "version": record.Version}, now)
}
