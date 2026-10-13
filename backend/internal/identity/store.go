package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const userColumns = "u.id::text,u.username,u.role,u.vip_expires_at,u.disabled,u.auth_version,u.created_at"
const identityLock = 73428102

// Store persists account state and atomically records sensitive mutations.
type Store struct{ pool *pgxpool.Pool }

// NewStore creates an identity store for an initialized database.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Role, &u.VIPExpiresAt, &u.Disabled, &u.AuthVersion, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("read account: %w", err)
	}
	return u, nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		slog.Warn("identity transaction rollback failed")
	}
}

func (s *Store) credential(ctx context.Context, username string) (User, string, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx, "SELECT "+userColumns+",u.password_hash FROM users u WHERE u.username=$1", username).
		Scan(&u.ID, &u.Username, &u.Role, &u.VIPExpiresAt, &u.Disabled, &u.AuthVersion, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("read credentials: %w", err)
	}
	return u, hash, nil
}

func (s *Store) insertUser(ctx context.Context, tx pgx.Tx, input CreateUserInput, hash string) (User, error) {
	u, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users AS u(username,password_hash,role,vip_expires_at)
		VALUES($1,$2,$3,$4) RETURNING `+userColumns, input.Username, hash, input.Role, input.VIPExpiresAt))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrConflict
	}
	return u, err
}

func recordAudit(ctx context.Context, tx pgx.Tx, actor *string, action, target, requestID string, metadata any, now time.Time) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_user_id,action,target_id,request_id,metadata,created_at)
		VALUES($1,$2,$3,$4,$5,$6)`, actor, action, target, requestID, data, now); err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

func (s *Store) bootstrap(ctx context.Context, input CreateUserInput, hash string, now time.Time) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin bootstrap: %w", err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", identityLock); err != nil {
		return User{}, fmt.Errorf("lock bootstrap: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role='admin')").Scan(&exists); err != nil {
		return User{}, fmt.Errorf("check administrator: %w", err)
	}
	if exists {
		return User{}, ErrAdministratorExists
	}
	u, err := s.insertUser(ctx, tx, input, hash)
	if err != nil {
		return User{}, err
	}
	if err := recordAudit(ctx, tx, nil, "admin.bootstrap", u.ID, "bootstrap", map[string]string{"role": "admin"}, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit bootstrap: %w", err)
	}
	return u, nil
}

func (s *Store) adminTx(ctx context.Context, actorID string, now time.Time) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin account change: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", identityLock); err != nil {
		rollback(tx)
		return nil, fmt.Errorf("lock account change: %w", err)
	}
	actor, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 FOR SHARE", actorID))
	if err != nil {
		rollback(tx)
		return nil, err
	}
	if err := Authorize(actor, ManageUsers, "", now); err != nil {
		rollback(tx)
		return nil, err
	}
	return tx, nil
}

func (s *Store) createUser(ctx context.Context, actorID string, input CreateUserInput, hash, requestID string, now time.Time) (User, error) {
	tx, err := s.adminTx(ctx, actorID, now)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)
	u, err := s.insertUser(ctx, tx, input, hash)
	if err != nil {
		return User{}, err
	}
	if err := recordAudit(ctx, tx, &actorID, "user.create", u.ID, requestID, map[string]any{"role": u.Role, "vip_expires_at": u.VIPExpiresAt}, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit account creation: %w", err)
	}
	return u, nil
}

func (s *Store) updateUser(ctx context.Context, actorID, targetID string, role *Role, expiry *time.Time, disabled *bool, requestID string, now time.Time) (User, error) {
	tx, err := s.adminTx(ctx, actorID, now)
	if err != nil {
		return User{}, err
	}
	defer rollback(tx)
	target, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 FOR UPDATE", targetID))
	if err != nil {
		return User{}, err
	}
	previous := target
	if role != nil {
		target.Role = *role
		target.VIPExpiresAt = expiry
	}
	if disabled != nil {
		target.Disabled = *disabled
	}
	if previous.Role == RoleAdmin && !previous.Disabled && (target.Role != RoleAdmin || target.Disabled) {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND NOT disabled").Scan(&count); err != nil {
			return User{}, fmt.Errorf("count administrators: %w", err)
		}
		if count <= 1 {
			return User{}, ErrLastAdministrator
		}
	}
	target, err = scanUser(tx.QueryRow(ctx, `UPDATE users AS u SET role=$2,vip_expires_at=$3,disabled=$4,
		auth_version=auth_version+1,updated_at=$5 WHERE id=$1 RETURNING `+userColumns,
		targetID, target.Role, target.VIPExpiresAt, target.Disabled, now))
	if err != nil {
		return User{}, err
	}
	action := "user.role.update"
	if disabled != nil {
		action = "user.disabled.update"
	}
	metadata := map[string]any{"old_role": previous.Role, "role": target.Role, "vip_expires_at": target.VIPExpiresAt, "disabled": target.Disabled}
	if err := recordAudit(ctx, tx, &actorID, action, targetID, requestID, metadata, now); err != nil {
		return User{}, err
	}
	if target.Disabled {
		if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", targetID); err != nil {
			return User{}, fmt.Errorf("revoke disabled sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit account change: %w", err)
	}
	return target, nil
}

// Principal is the authenticated session and its freshly loaded account.
type Principal struct {
	User      User
	SessionID string
}

// SessionInfo is safe metadata about a device session.
type SessionInfo struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

func (s *Store) createSession(ctx context.Context, userID string, hash []byte, now, expires time.Time) (Principal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf("begin session: %w", err)
	}
	defer rollback(tx)
	u, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 AND NOT u.disabled FOR SHARE", userID))
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	var id string
	if err := tx.QueryRow(ctx, "INSERT INTO sessions(user_id,token_hash,created_at,expires_at) VALUES($1,$2,$3,$4) RETURNING id::text", userID, hash, now, expires).Scan(&id); err != nil {
		return Principal{}, fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, fmt.Errorf("commit session: %w", err)
	}
	return Principal{User: u, SessionID: id}, nil
}

func (s *Store) authenticate(ctx context.Context, hash []byte, now time.Time) (Principal, error) {
	var p Principal
	err := s.pool.QueryRow(ctx, `SELECT `+userColumns+`,s.id::text FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at>$2 AND NOT u.disabled`, hash, now).
		Scan(&p.User.ID, &p.User.Username, &p.User.Role, &p.User.VIPExpiresAt, &p.User.Disabled, &p.User.AuthVersion, &p.User.CreatedAt, &p.SessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("read session: %w", err)
	}
	return p, nil
}

// readTx holds the current actor row until a private read completes, so callers
// cannot reuse an account snapshot after its role or enabled state changes.
func (s *Store) readTx(ctx context.Context, actorID string, action Action, ownerID string, now time.Time) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin authorized read: %w", err)
	}
	actor, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 FOR SHARE", actorID))
	if err != nil {
		rollback(tx)
		return nil, err
	}
	if err := Authorize(actor, action, ownerID, now); err != nil {
		rollback(tx)
		return nil, err
	}
	return tx, nil
}

func (s *Store) sessions(ctx context.Context, userID, currentID string, now time.Time) ([]SessionInfo, error) {
	tx, err := s.readTx(ctx, userID, ManageSessions, userID, now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT id::text,user_id::text,created_at,expires_at FROM sessions WHERE user_id=$1 AND expires_at>$2 ORDER BY created_at DESC", userID, now)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	result := make([]SessionInfo, 0)
	for rows.Next() {
		var item SessionInfo
		if err := rows.Scan(&item.ID, &item.UserID, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return nil, fmt.Errorf("read session metadata: %w", err)
		}
		item.Current = item.ID == currentID
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return result, nil
}

func (s *Store) revokeSession(ctx context.Context, actorID, targetID, requestID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin session revoke: %w", err)
	}
	defer rollback(tx)
	actor, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1 FOR SHARE", actorID))
	if err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRow(ctx, "SELECT user_id::text FROM sessions WHERE id=$1 FOR UPDATE", targetID).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read session owner: %w", err)
	}
	if err := Authorize(actor, ManageSessions, owner, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id=$1", targetID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := recordAudit(ctx, tx, &actorID, "session.revoke", targetID, requestID, map[string]string{}, now); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit session revoke: %w", err)
	}
	return nil
}

func (s *Store) users(ctx context.Context, actorID string, limit, offset int, now time.Time) ([]User, error) {
	tx, err := s.readTx(ctx, actorID, ManageUsers, "", now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT "+userColumns+" FROM users u ORDER BY u.created_at,u.id LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	result := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return result, nil
}

// AuditEvent contains the actor and safe metadata for an audited operation.
type AuditEvent struct {
	ID          int64           `json:"id"`
	ActorUserID *string         `json:"actor_user_id"`
	Action      string          `json:"action"`
	TargetID    string          `json:"target_id"`
	RequestID   string          `json:"request_id"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (s *Store) audit(ctx context.Context, actorID string, limit, offset int, now time.Time) ([]AuditEvent, error) {
	tx, err := s.readTx(ctx, actorID, ReadAudit, "", now)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, "SELECT id,actor_user_id::text,action,target_id,request_id,metadata,created_at FROM audit_events ORDER BY id DESC LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	result := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.ActorUserID, &event.Action, &event.TargetID, &event.RequestID, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("read audit event: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return result, nil
}
