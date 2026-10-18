package notification

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

// DispatchOne commits sending before any network call. An interrupted sending record
// becomes unknown on recovery and is never automatically attempted again.
func (s *Store) DispatchOne(ctx context.Context, sender *Sender, now time.Time) (bool, error) {
	if _, err := s.pool.Exec(ctx, `UPDATE notification_deliveries SET state='delivery_unknown',error_code='interrupted_send',updated_at=$1 WHERE state='sending' AND sending_at<$2`, now, now.Add(-30*time.Second)); err != nil {
		return false, err
	}
	var id, batchID string
	var attempt int
	err := s.pool.QueryRow(ctx, `WITH candidate AS(SELECT id FROM notification_deliveries WHERE state='queued' AND available_at<=$1 ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
UPDATE notification_deliveries d SET state='sending',attempts=attempts+1,sending_at=$1,updated_at=$1 FROM candidate c WHERE d.id=c.id RETURNING d.id::text,d.batch_id::text,d.attempts`, now).Scan(&id, &batchID, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var owner, kind, mode, profileID string
	var subRevision, channelRevision int64
	var raw []byte
	var cutoff time.Time
	err = s.pool.QueryRow(ctx, `SELECT owner_user_id::text,kind,mode,COALESCE(profile_version_id::text,''),subscription_revision,channel_revision,items,cutoff FROM notification_batches WHERE id=$1`, batchID).Scan(&owner, &kind, &mode, &profileID, &subRevision, &channelRevision, &raw, &cutoff)
	if err != nil {
		return true, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer task.Rollback(tx)
	// All user operations acquire account share locks. An update lock here orders a
	// send against simultaneous account changes, applications and private-resource edits.
	var lockedOwner string
	if err = tx.QueryRow(ctx, "SELECT id::text FROM users WHERE id=$1 FOR UPDATE", owner).Scan(&lockedOwner); err != nil {
		return true, err
	}
	var sendingState string
	if err = tx.QueryRow(ctx, "SELECT state FROM notification_deliveries WHERE id=$1 FOR UPDATE", id).Scan(&sendingState); err != nil {
		return true, err
	}
	if sendingState != "sending" {
		return true, nil
	}
	finish := func(out Outcome) error {
		state := out.State
		available := now
		if state == "retry" {
			if attempt >= 3 {
				state = "failed"
			} else {
				state = "queued"
				available = now.Add(time.Duration(attempt*attempt) * time.Minute)
			}
		}
		_, err := tx.Exec(ctx, `UPDATE notification_deliveries SET state=$2,error_code=$3,payload_bytes=$4,available_at=$5,updated_at=$6 WHERE id=$1 AND state='sending'`, id, state, out.Code, out.Bytes, available, now)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err = identity.CheckAccess(ctx, tx, owner, identity.ManageSubscriptions, "", now); err != nil {
		if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
			return true, finish(Outcome{State: "cancelled", Code: "account_unavailable"})
		}
		return true, err
	}
	channel, encrypted, err := s.channel(ctx, tx, owner)
	if err != nil {
		return true, err
	}
	if !channel.Configured || !channel.Enabled || channel.Revision != channelRevision {
		return true, finish(Outcome{State: "cancelled", Code: "channel_changed"})
	}
	var items []Item
	if err = json.Unmarshal(raw, &items); err != nil {
		return true, err
	}
	if kind == "digest" {
		sub, _, err := subscription(ctx, tx, owner)
		if err != nil {
			return true, err
		}
		if !sub.Enabled || sub.Revision != subRevision || sub.Mode != mode {
			return true, finish(Outcome{State: "cancelled", Code: "subscription_changed"})
		}
		if mode == "personalized" {
			if _, err = identity.CheckAccess(ctx, tx, owner, identity.ReadRecommendations, "", now); err != nil {
				return true, finish(Outcome{State: "cancelled", Code: "permission_changed"})
			}
			current, err := currentProfile(ctx, tx, owner, now)
			if err != nil {
				return true, err
			}
			if current == "" || current != profileID {
				return true, finish(Outcome{State: "cancelled", Code: "profile_changed"})
			}
		}
		var ids []string
		for _, item := range items {
			ids = append(ids, item.JobID)
		}
		rows, err := tx.Query(ctx, "SELECT id FROM jobs WHERE id::text=ANY($1) ORDER BY id FOR SHARE", ids)
		if err != nil {
			return true, err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return true, err
		}
		jobs, err := catalog.LoadJobs(ctx, tx, ids, now)
		if err != nil {
			return true, err
		}
		filtered := []Item{}
		for _, item := range items {
			job, ok := jobs[item.JobID]
			if !ok || !job.Published || job.PublicStatus == "closed" || job.Version != item.JobVersion {
				continue
			}
			var submitted bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM applications WHERE owner_user_id=$1 AND job_id=$2 AND status<>'draft')", owner, item.JobID).Scan(&submitted); err != nil {
				return true, err
			}
			if submitted {
				continue
			}
			if mode == "personalized" {
				var variants []catalog.Variant
				for _, variant := range job.Variants {
					if slices.Contains(item.VariantKeys, variant.Key) {
						variants = append(variants, variant)
					}
				}
				current, _, err := s.personalVariants(ctx, tx, owner, profileID, job, variants, now)
				if err != nil {
					return true, err
				}
				if len(current) == 0 {
					continue
				}
				// A partial invalidation cannot leave locations belonging to a removed direction.
				item.VariantKeys = []string{}
				item.Locations = []string{}
				for _, variant := range current {
					item.VariantKeys = append(item.VariantKeys, variant.Key)
					for _, place := range variant.Locations {
						if !slices.Contains(item.Locations, place.City) {
							item.Locations = append(item.Locations, place.City)
						}
					}
				}
			}
			filtered = append(filtered, item)
		}
		items = filtered
		if len(items) == 0 {
			return true, finish(Outcome{State: "cancelled", Code: "no_current_opportunities"})
		}
	}
	text := Render(items, cutoff, kind == "test")
	if !s.sendEnabled {
		data, err := encodeMessage(text, "", now)
		if err != nil {
			return true, err
		}
		return true, finish(Outcome{State: "simulated", Code: "real_sending_disabled", Bytes: len(data)})
	}
	secret, err := s.cipher.open(owner, encrypted)
	if err != nil {
		return true, finish(Outcome{State: "failed", Code: "channel_unavailable"})
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return true, finish(sender.Send(callCtx, secret, text, now))
}

// ResolveUnknown requires a current administrator and an explicit human conclusion.
func (s *Store) ResolveUnknown(ctx context.Context, actor, id, decision, requestID string, now time.Time) error {
	if decision != "received" && decision != "not_received" {
		return identity.ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer task.Rollback(tx)
	if _, err = identity.CheckAccess(ctx, tx, actor, identity.ManageUsers, "", now); err != nil {
		return err
	}
	var delivery string
	var attempts int
	err = tx.QueryRow(ctx, "SELECT id::text,attempts FROM notification_deliveries WHERE batch_id=$1 AND state='delivery_unknown' FOR UPDATE", id).Scan(&delivery, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrNotFound
	}
	if err != nil {
		return err
	}
	state := "sent"
	if decision == "not_received" {
		state = "queued"
		if attempts >= 3 {
			state = "failed"
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE notification_deliveries SET state=$2,error_code='admin_confirmed',available_at=$3,updated_at=$3 WHERE id=$1", delivery, state, now); err != nil {
		return err
	}
	if err = identity.AppendAudit(ctx, tx, actor, "notification.delivery.resolve", id, requestID, map[string]string{"decision": decision}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
