package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobassistant/backend/internal/identity"
)

// Shanghai is the product's current UTC+8 scheduling zone.
var Shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

// DayTimes returns the local scheduling date, collection time and digest cutoff.
func DayTimes(now time.Time) (string, time.Time, time.Time) {
	local := now.In(Shanghai)
	start := time.Date(local.Year(), local.Month(), local.Day(), 8, 0, 0, 0, Shanghai)
	return start.Format("2006-01-02"), start, start.Add(67 * time.Minute)
}

// Collection is a public source task. Lease tokens are not exposed through the API.
type Collection struct {
	ID, SourceCode, Domain, State string
	Attempts                      int
	RecoveryRound                 int
	LeaseToken                    string `json:"-"`
}

// CollectionStore owns the public-source schedule independently of private resume tasks.
type CollectionStore struct {
	pool              *pgxpool.Pool
	allowed, excluded []string
}

// NewCollectionStore uses the shared database for persistent scheduling and leases.
func NewCollectionStore(pool *pgxpool.Pool) *CollectionStore { return &CollectionStore{pool: pool} }

// WithSources returns a queue view for one runtime without changing shared plans.
// Empty allowed means all sources; exclusions apply only to this view's claims.
func (s *CollectionStore) WithSources(allowed, excluded []string) *CollectionStore {
	return &CollectionStore{pool: s.pool, allowed: slices.Clone(allowed), excluded: slices.Clone(excluded)}
}

// Plan idempotently enqueues today's sources and bounded transient recovery.
func (s *CollectionStore) Plan(ctx context.Context, now time.Time) error {
	day, start, _ := DayTimes(now)
	if now.Before(start) {
		return nil
	}
	rows, err := s.pool.Query(ctx, "SELECT code,official_url FROM sources WHERE enabled ORDER BY code")
	if err != nil {
		return err
	}
	var entries [][2]string
	for rows.Next() {
		var entry [2]string
		if err := rows.Scan(&entry[0], &entry[1]); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := s.Enqueue(ctx, "daily:"+day+":"+entry[0], entry[0], entry[1], now); err != nil {
			return err
		}
	}
	return s.planRecoveries(ctx, now)
}

// Enqueue uses a caller-selected idempotency key; manual keys use a fresh random ID.
func (s *CollectionStore) Enqueue(ctx context.Context, key, source, officialURL string, now time.Time) (string, error) {
	u, err := url.Parse(officialURL)
	if err != nil || u.Hostname() == "" {
		return "", identity.ErrInvalidInput
	}
	var id string
	err = s.pool.QueryRow(ctx, `INSERT INTO scheduled_collections(schedule_key,source_code,domain,available_at,created_at,updated_at)
VALUES($1,$2,$3,$4,$4,$4) ON CONFLICT(schedule_key) DO UPDATE SET schedule_key=excluded.schedule_key RETURNING id::text`, key, source, u.Hostname(), now).Scan(&id)
	return id, err
}

// Claim recovers expired leases and takes at most one task per active domain.
func (s *CollectionStore) Claim(ctx context.Context, now time.Time) (Collection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Collection{}, err
	}
	defer Rollback(tx)
	if _, err := tx.Exec(ctx, `UPDATE scheduled_collections t SET state='cancelled',error_code='source_paused',updated_at=$1
WHERE (state='queued' OR(state='running' AND lease_until<=$1)) AND EXISTS(SELECT 1 FROM sources s WHERE s.code=t.source_code AND NOT s.enabled)`, now); err != nil {
		return Collection{}, err
	}
	if _, err := tx.Exec(ctx, "UPDATE scheduled_collections SET state='failed',error_code='attempts_exhausted',updated_at=$1 WHERE attempts>=3 AND (state='queued' OR (state='running' AND lease_until<=$1))", now); err != nil {
		return Collection{}, err
	}
	if _, err := cancelInvalidRecoveries(ctx, tx, now, ""); err != nil {
		return Collection{}, err
	}
	var j Collection
	err = tx.QueryRow(ctx, `SELECT t.id::text,t.source_code,t.domain,t.attempts,t.recovery_round FROM scheduled_collections t JOIN sources s ON s.code=t.source_code
WHERE s.enabled AND t.available_at<=$1 AND (t.state='queued' OR (t.state='running' AND t.lease_until<=$1))
AND (COALESCE(cardinality($2::text[]),0)=0 OR t.source_code=ANY($2))
AND NOT(t.source_code=ANY(COALESCE($3::text[],'{}'::text[])))
AND (t.recovery_round=0 OR NOT EXISTS(SELECT 1 FROM scheduled_collections other WHERE other.source_code=t.source_code AND other.id<>t.id AND other.state IN ('queued','running')))
AND (t.recovery_round=0 OR s.last_status<>'running')
AND NOT EXISTS(SELECT 1 FROM scheduled_collections active WHERE active.domain=t.domain AND active.state='running' AND active.lease_until>$1)
ORDER BY t.available_at,t.created_at,t.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`, now, s.allowed, s.excluded).Scan(&j.ID, &j.SourceCode, &j.Domain, &j.Attempts, &j.RecoveryRound)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return j, err
		}
		return j, identity.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if j.RecoveryRound > 0 {
		var source string
		if err = tx.QueryRow(ctx, "SELECT code FROM sources WHERE code=$1 FOR UPDATE", j.SourceCode).Scan(&source); err != nil {
			return j, err
		}
		cancelled, checkErr := cancelInvalidRecoveries(ctx, tx, now, j.ID)
		if checkErr != nil {
			return j, checkErr
		}
		waiting, checkErr := recoveryWaiting(ctx, tx, j.ID)
		if checkErr != nil {
			return j, checkErr
		}
		if cancelled > 0 || waiting {
			if err = tx.Commit(ctx); err != nil {
				return j, err
			}
			return j, identity.ErrNotFound
		}
	}
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1,73428401))", j.Domain).Scan(&locked); err != nil {
		return j, err
	}
	if !locked {
		if err = tx.Commit(ctx); err != nil {
			return j, err
		}
		return j, identity.ErrNotFound
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_collections WHERE domain=$1 AND id<>$2 AND state='running' AND lease_until>$3)`, j.Domain, j.ID, now).Scan(&busy); err != nil {
		return j, err
	}
	if busy {
		if err = tx.Commit(ctx); err != nil {
			return j, err
		}
		return j, identity.ErrNotFound
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return j, err
	}
	j.LeaseToken = hex.EncodeToString(raw[:])
	j.Attempts++
	j.State = "running"
	_, err = tx.Exec(ctx, `UPDATE scheduled_collections SET state='running',attempts=$2,lease_token=$3,lease_until=$4,updated_at=$5 WHERE id=$1`, j.ID, j.Attempts, j.LeaseToken, now.Add(20*time.Minute), now)
	if err != nil {
		return j, err
	}
	err = tx.Commit(ctx)
	return j, err
}

// Finish accepts only the current unexpired token and applies bounded retry delays.
func (s *CollectionStore) Finish(ctx context.Context, j Collection, status, code string, fetched, published int, now time.Time) error {
	state := status
	if state != "succeeded" {
		state = "failed"
		if j.Attempts < 3 && slices.Contains(retryableCollectionCodes, code) {
			state = "queued"
		}
	}
	_, err := s.pool.Exec(ctx, `UPDATE scheduled_collections SET state=$3,error_code=$4,fetched=$5,published=$6,available_at=$7,
lease_until=NULL,lease_token='',updated_at=$8 WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until>$8`, j.ID, j.LeaseToken, state, code, fetched, published, now.Add(time.Duration(j.Attempts*j.Attempts)*time.Minute), now)
	return err
}
