package notification

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// Options supplies independent channel encryption and the explicit outbound switch.
type Options struct {
	Key                  []byte
	Origin, ModelVersion string
	SendEnabled          bool
}

// Store owns subscriptions, station snapshots and persistent delivery state.
type Store struct {
	pool                 *pgxpool.Pool
	cipher               *channelCipher
	origin, modelVersion string
	sendEnabled          bool
}

// NewStore validates configuration; an absent key still permits station-only digests.
func NewStore(pool *pgxpool.Pool, options Options) (*Store, error) {
	cipher, err := newCipher(options.Key)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(options.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, identity.ErrInvalidInput
	}
	return &Store{pool: pool, cipher: cipher, origin: strings.TrimRight(options.Origin, "/"), modelVersion: options.ModelVersion, sendEnabled: options.SendEnabled}, nil
}

func (s *Store) authorized(ctx context.Context, scope Scope, now time.Time) (pgx.Tx, string, error) {
	owner := scope.ActorID
	if scope.Admin {
		owner = scope.OwnerID
		if owner == "" {
			return nil, "", identity.ErrInvalidInput
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	if scope.Admin {
		if _, err = identity.CheckAccess(ctx, tx, scope.ActorID, identity.ManageUsers, "", now); err != nil {
			task.Rollback(tx)
			return nil, "", err
		}
	}
	if _, err = identity.CheckAccess(ctx, tx, scope.ActorID, identity.ManageSubscriptions, owner, now); err != nil {
		task.Rollback(tx)
		return nil, "", err
	}
	if !scope.Admin {
		if _, err = identity.CheckAccess(ctx, tx, owner, identity.ManageSubscriptions, "", now); err != nil {
			task.Rollback(tx)
			return nil, "", err
		}
	} else {
		var found string
		if err = tx.QueryRow(ctx, "SELECT id::text FROM users WHERE id=$1 FOR SHARE", owner).Scan(&found); err != nil {
			task.Rollback(tx)
			return nil, "", err
		}
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,73428402))", owner); err != nil {
		task.Rollback(tx)
		return nil, "", err
	}
	return tx, owner, nil
}

func subscription(ctx context.Context, tx pgx.Tx, owner string) (Subscription, *time.Time, error) {
	value := Subscription{Mode: "filtered"}
	var cutoff *time.Time
	err := tx.QueryRow(ctx, "SELECT filters_query,mode,enabled,revision,last_cutoff FROM subscriptions WHERE owner_user_id=$1 FOR UPDATE", owner).Scan(&value.FiltersQuery, &value.Mode, &value.Enabled, &value.Revision, &cutoff)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, nil, nil
	}
	return value, cutoff, err
}

// Subscription returns the owner's settings or disabled defaults.
func (s *Store) Subscription(ctx context.Context, scope Scope, now time.Time) (Subscription, error) {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return Subscription{}, err
	}
	defer task.Rollback(tx)
	value, _, err := subscription(ctx, tx, owner)
	if err != nil {
		return value, err
	}
	return value, tx.Commit(ctx)
}
func parseQuery(raw string) (catalog.Filter, string, error) {
	if len(raw) > 4096 {
		return catalog.Filter{}, "", identity.ErrInvalidInput
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return catalog.Filter{}, "", identity.ErrInvalidInput
	}
	for key := range values {
		if !slices.Contains(strings.Split("q|company|direction|major|city|industry|nature|tier|graduation_year|recruitment_type|written_test|deadline_before|status|include_unknown|sort|limit|offset|view", "|"), key) {
			return catalog.Filter{}, "", identity.ErrInvalidInput
		}
	}
	for _, key := range []string{"sort", "limit", "offset", "view"} {
		values.Del(key)
	}
	f, err := catalog.ParseFilter(values)
	return f, values.Encode(), err
}

func cancelQueued(ctx context.Context, tx pgx.Tx, owner, code string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE notification_deliveries d SET state='cancelled',error_code=$2,updated_at=$3 FROM notification_batches b WHERE b.id=d.batch_id AND b.owner_user_id=$1 AND d.state='queued'`, owner, code, now)
	return err
}

// SaveSubscription validates filters and current permissions before a versioned edit.
func (s *Store) SaveSubscription(ctx context.Context, scope Scope, input Subscription, requestID string, now time.Time) (Subscription, error) {
	_, query, err := parseQuery(input.FiltersQuery)
	if err != nil || (input.Mode != "filtered" && input.Mode != "personalized") {
		return Subscription{}, identity.ErrInvalidInput
	}
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return Subscription{}, err
	}
	defer task.Rollback(tx)
	if input.Mode == "personalized" && input.Enabled {
		if _, err = identity.CheckAccess(ctx, tx, owner, identity.CreateRecommendations, "", now); err != nil {
			return Subscription{}, err
		}
	}
	previous, _, err := subscription(ctx, tx, owner)
	if err != nil {
		return Subscription{}, err
	}
	if input.Revision != previous.Revision {
		return Subscription{}, ErrVersionConflict
	}
	input.FiltersQuery = query
	input.Revision++
	_, err = tx.Exec(ctx, `INSERT INTO subscriptions(owner_user_id,filters_query,mode,enabled,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)
ON CONFLICT(owner_user_id) DO UPDATE SET filters_query=$2,mode=$3,enabled=$4,revision=$5,last_cutoff=NULL,updated_at=$6`, owner, query, input.Mode, input.Enabled, input.Revision, now)
	if err != nil {
		return Subscription{}, err
	}
	if err = cancelQueued(ctx, tx, owner, "subscription_changed", now); err != nil {
		return Subscription{}, err
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "notification.subscription.update", owner, requestID, map[string]any{"enabled": input.Enabled, "mode": input.Mode}, now); err != nil {
		return Subscription{}, err
	}
	return input, tx.Commit(ctx)
}

func (s *Store) channel(ctx context.Context, tx pgx.Tx, owner string) (ChannelView, []byte, error) {
	value := ChannelView{KeyConfigured: s.cipher != nil, SendEnabled: s.sendEnabled}
	var encrypted []byte
	err := tx.QueryRow(ctx, "SELECT encrypted_config,has_secret,enabled,revision FROM notification_channels WHERE owner_user_id=$1 FOR SHARE", owner).Scan(&encrypted, &value.HasSecret, &value.Enabled, &value.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, nil, nil
	}
	value.Configured = len(encrypted) > 0
	return value, encrypted, err
}

// Channel returns only the owner's non-secret receiver metadata.
func (s *Store) Channel(ctx context.Context, scope Scope, now time.Time) (ChannelView, error) {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return ChannelView{}, err
	}
	defer task.Rollback(tx)
	value, _, err := s.channel(ctx, tx, owner)
	if err != nil {
		return value, err
	}
	return value, tx.Commit(ctx)
}

// SaveChannel encrypts a validated receiver and invalidates queued older configurations.
func (s *Store) SaveChannel(ctx context.Context, scope Scope, input ChannelInput, requestID string, now time.Time) (ChannelView, error) {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return ChannelView{}, err
	}
	defer task.Rollback(tx)
	previous, encrypted, err := s.channel(ctx, tx, owner)
	if err != nil {
		return previous, err
	}
	if previous.Revision != input.Revision {
		return previous, ErrVersionConflict
	}
	var secret ChannelSecret
	if previous.Configured {
		secret, err = s.cipher.open(owner, encrypted)
		if err != nil {
			return previous, err
		}
	}
	if input.Webhook != nil {
		secret.Webhook = strings.TrimSpace(*input.Webhook)
	}
	if input.Secret != nil {
		secret.Secret = strings.TrimSpace(*input.Secret)
	}
	if !validWebhook(secret.Webhook) || len(secret.Secret) > 256 {
		return previous, identity.ErrInvalidInput
	}
	encrypted, err = s.cipher.seal(owner, secret)
	if err != nil {
		return previous, err
	}
	revision := previous.Revision + 1
	_, err = tx.Exec(ctx, `INSERT INTO notification_channels(owner_user_id,encrypted_config,has_secret,enabled,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)
ON CONFLICT(owner_user_id) DO UPDATE SET encrypted_config=$2,has_secret=$3,enabled=$4,revision=$5,updated_at=$6`, owner, encrypted, secret.Secret != "", input.Enabled, revision, now)
	if err != nil {
		return previous, err
	}
	if err = cancelQueued(ctx, tx, owner, "channel_changed", now); err != nil {
		return previous, err
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "notification.channel.update", owner, requestID, map[string]bool{"enabled": input.Enabled, "has_secret": secret.Secret != ""}, now); err != nil {
		return previous, err
	}
	return ChannelView{Configured: true, Enabled: input.Enabled, HasSecret: secret.Secret != "", Revision: revision, KeyConfigured: s.cipher != nil, SendEnabled: s.sendEnabled}, tx.Commit(ctx)
}

// ClearChannel erases credentials while keeping the revision monotonic.
func (s *Store) ClearChannel(ctx context.Context, scope Scope, revision int64, requestID string, now time.Time) error {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	value, _, err := s.channel(ctx, tx, owner)
	if err != nil {
		return err
	}
	if value.Revision != revision {
		return ErrVersionConflict
	}
	_, err = tx.Exec(ctx, "UPDATE notification_channels SET encrypted_config=''::bytea,enabled=false,has_secret=false,revision=revision+1,updated_at=$2 WHERE owner_user_id=$1", owner, now)
	if err != nil {
		return err
	}
	if err = cancelQueued(ctx, tx, owner, "channel_removed", now); err != nil {
		return err
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "notification.channel.clear", owner, requestID, map[string]bool{}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// List returns paginated station history after checking current owner access.
func (s *Store) List(ctx context.Context, scope Scope, limit, offset int, now time.Time) ([]Batch, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, identity.ErrInvalidInput
	}
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	defer task.Rollback(tx)
	rows, err := tx.Query(ctx, `SELECT b.id::text,b.owner_user_id::text,b.kind,b.mode,b.cutoff,COALESCE(d.state,b.state),COALESCE(d.error_code,''),COALESCE(d.attempts,0),b.items,b.sources,b.created_at
FROM notification_batches b LEFT JOIN notification_deliveries d ON d.batch_id=b.id WHERE b.owner_user_id=$1 ORDER BY b.created_at DESC,b.id DESC LIMIT $2 OFFSET $3`, owner, limit, offset)
	if err != nil {
		return nil, err
	}
	items := []Batch{}
	for rows.Next() {
		var b Batch
		var data, sources []byte
		if err = rows.Scan(&b.ID, &b.OwnerID, &b.Kind, &b.Mode, &b.Cutoff, &b.State, &b.ErrorCode, &b.Attempts, &data, &sources, &b.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(data, &b.Items); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(sources, &b.Sources); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}

// Test queues a rate-limited constant message without résumé or application data.
func (s *Store) Test(ctx context.Context, scope Scope, requestID string, now time.Time) (string, error) {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return "", err
	}
	defer task.Rollback(tx)
	channel, _, err := s.channel(ctx, tx, owner)
	if err != nil {
		return "", err
	}
	if !channel.Configured || !channel.Enabled {
		return "", identity.ErrInvalidInput
	}
	var recent int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM notification_batches WHERE owner_user_id=$1 AND kind='test' AND created_at>$2", owner, now.Add(-time.Minute)).Scan(&recent); err != nil {
		return "", err
	}
	if recent >= 3 {
		return "", identity.ErrInvalidInput
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO notification_batches(owner_user_id,batch_key,cutoff,kind,mode,subscription_revision,channel_revision,state,created_at) VALUES($1,'test:'||gen_random_uuid()::text,$2,'test','filtered',0,$3,'queued',$2) RETURNING id::text`, owner, now, channel.Revision).Scan(&id)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO notification_deliveries(batch_id,available_at,created_at,updated_at) VALUES($1,$2,$2,$2)`, id, now); err != nil {
		return "", err
	}
	if err = identity.AppendAudit(ctx, tx, scope.ActorID, "notification.test.request", id, requestID, map[string]bool{"real_sending": s.sendEnabled}, now); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}
