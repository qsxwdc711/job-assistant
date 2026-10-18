package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/task"
)

type snapshot struct {
	job       catalog.Job
	changedAt time.Time
}

func currentProfile(ctx context.Context, tx pgx.Tx, owner string, at time.Time) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT v.id::text FROM current_profiles c JOIN profile_versions v ON v.id=c.profile_version_id
JOIN resumes r ON r.id=v.resume_id WHERE c.owner_user_id=$1 AND r.owner_user_id=$1 AND r.deleted_at IS NULL AND v.created_at<=$2 FOR SHARE OF c,v,r`, owner, at).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) personalVariants(ctx context.Context, tx pgx.Tx, owner, profile string, job catalog.Job, variants []catalog.Variant, cutoff time.Time) ([]catalog.Variant, bool, error) {
	var result []catalog.Variant
	uncertain := false
	for _, variant := range variants {
		var status string
		err := tx.QueryRow(ctx, `SELECT eligibility->>'status' FROM recommendations WHERE owner_user_id=$1 AND profile_version_id=$2
AND job_id=$3 AND job_version=$4 AND variant_key=$5 AND variant_hash=$6 AND rule_version=$7
AND (model_version='rules-only' OR model_version=$8) AND semantic_status IN ('succeeded','not_requested') AND updated_at<=$9
AND eligibility->>'status' IN ('eligible','needs_confirmation') ORDER BY updated_at DESC LIMIT 1`, owner, profile, job.ID, job.Version, variant.Key, matching.VariantHash(variant), matching.RuleVersion, s.modelVersion, cutoff).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		result = append(result, variant)
		uncertain = uncertain || status == "needs_confirmation"
	}
	return result, uncertain, nil
}

func snapshots(ctx context.Context, tx pgx.Tx, owner string, cutoff, now time.Time) ([]snapshot, error) {
	rows, err := tx.Query(ctx, `SELECT j.id::text,v.version,v.data,v.created_at,c.name,c.industry,c.nature,c.tiers FROM jobs j
JOIN companies c ON c.code=j.company_code JOIN LATERAL(SELECT version,data,created_at FROM job_versions WHERE job_id=j.id AND created_at<=$2 ORDER BY version DESC LIMIT 1)v ON true
WHERE j.published AND j.public_status<>'closed' AND (j.deadline_at IS NULL OR j.deadline_at>$3)
AND NOT EXISTS(SELECT 1 FROM applications a WHERE a.owner_user_id=$1 AND a.job_id=j.id AND a.status<>'draft') ORDER BY j.id`, owner, cutoff, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []snapshot
	for rows.Next() {
		var item snapshot
		var raw []byte
		err = rows.Scan(&item.job.ID, &item.job.Version, &raw, &item.changedAt, &item.job.CompanyName, &item.job.Industry, &item.job.Nature, &item.job.Tiers)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item.job.Document); err != nil {
			return nil, err
		}
		item.job.Published = true
		values = append(values, item)
	}
	return values, rows.Err()
}

func sourceStates(ctx context.Context, tx pgx.Tx, cutoff time.Time) (map[string]string, error) {
	_, start, _ := task.DayTimes(cutoff)
	rows, err := tx.Query(ctx, `SELECT s.code,COALESCE((SELECT CASE WHEN r.finished_at<=$2 THEN r.status ELSE 'not_completed' END FROM ingest_runs r WHERE r.source_code=s.code AND r.started_at>=$1 AND r.started_at<=$2 ORDER BY r.started_at DESC LIMIT 1),'not_completed') FROM sources s WHERE s.enabled ORDER BY s.code`, start, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var code, state string
		if err = rows.Scan(&code, &state); err != nil {
			return nil, err
		}
		values[code] = state
	}
	return values, rows.Err()
}

// Generate creates one system-owned digest transaction; HTTP callers enqueue a request instead.
func (s *Store) Generate(ctx context.Context, owner, key string, cutoff, now time.Time) (string, error) {
	return s.generate(ctx, Scope{ActorID: owner}, key, cutoff, now)
}
func (s *Store) generate(ctx context.Context, scope Scope, key string, cutoff, now time.Time) (string, error) {
	tx, owner, err := s.authorized(ctx, scope, now)
	if err != nil {
		return "", err
	}
	defer task.Rollback(tx)
	if _, err = identity.CheckAccess(ctx, tx, owner, identity.ManageSubscriptions, "", now); err != nil {
		return "", err
	}
	sub, last, err := subscription(ctx, tx, owner)
	if err != nil {
		return "", err
	}
	if !sub.Enabled {
		return "", nil
	}
	channel, _, err := s.channel(ctx, tx, owner)
	if err != nil {
		return "", err
	}
	var existing string
	err = tx.QueryRow(ctx, "SELECT id::text FROM notification_batches WHERE owner_user_id=$1 AND batch_key=$2 AND channel_revision=$3", owner, key, channel.Revision).Scan(&existing)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if last != nil && !cutoff.After(*last) {
		return "", nil
	}
	profileID := ""
	if sub.Mode == "personalized" {
		if _, err = identity.CheckAccess(ctx, tx, owner, identity.ReadRecommendations, "", now); err != nil {
			return "", err
		}
		profileID, err = currentProfile(ctx, tx, owner, cutoff)
		if err != nil {
			return "", err
		}
	}
	filter, _, err := parseQuery(sub.FiltersQuery)
	if err != nil {
		return "", err
	}
	values, err := snapshots(ctx, tx, owner, cutoff, now)
	if err != nil {
		return "", err
	}
	items := []Item{}
	var eventIDs []string
	for _, value := range values {
		job := value.job
		variants := catalog.MatchingVariants(job, filter, now)
		uncertain := job.PublicStatus == "unknown" || job.MinimumEducation == "unknown" || len(job.GraduationRules) == 0
		if sub.Mode == "personalized" {
			if profileID == "" {
				continue
			}
			variants, uncertain, err = s.personalVariants(ctx, tx, owner, profileID, job, variants, cutoff)
			if err != nil {
				return "", err
			}
		}
		if len(variants) == 0 || job.PublicStatus == "closed" {
			continue
		}
		type event struct {
			key, kind string
			priority  int
		}
		var events []event
		suffix := sub.Mode + ":" + profileID
		firstMatch := last == nil
		if sub.Mode == "personalized" && !firstMatch {
			var known bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_events WHERE owner_user_id=$1 AND job_id=$2 AND kind IN ('new','changed') AND event_key LIKE $3)`, owner, job.ID, "%:"+suffix).Scan(&known); err != nil {
				return "", err
			}
			// A recommendation can become usable after an empty digest, or after
			// changing the current profile, without the public job changing.
			firstMatch = !known
		}
		if firstMatch {
			events = append(events, event{key: "new:" + job.ID + ":" + strconv.FormatInt(job.Version, 10) + ":" + suffix, kind: "new", priority: 4})
		} else if value.changedAt.After(*last) {
			var previous []byte
			err = tx.QueryRow(ctx, "SELECT data FROM job_versions WHERE job_id=$1 AND created_at<=$2 ORDER BY version DESC LIMIT 1", job.ID, *last).Scan(&previous)
			kind, priority := "new", 4
			if err == nil {
				var before catalog.Document
				if err = json.Unmarshal(previous, &before); err != nil {
					return "", err
				}
				changed, err := importantChange(before, job.Document)
				if err != nil {
					return "", err
				}
				if !changed {
					kind = ""
				} else {
					kind, priority = "changed", 3
				}
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", err
			}
			if kind != "" {
				events = append(events, event{key: kind + ":" + job.ID + ":" + strconv.FormatInt(job.Version, 10) + ":" + suffix, kind: kind, priority: priority})
			}
		}
		if window := deadlineWindow(job.DeadlineAt, cutoff); window != 0 {
			priority := slices.Index([]int{1, 3, 7}, window)
			events = append(events, event{key: fmt.Sprintf("deadline:%s:%s:%d", job.ID, job.DeadlineAt.UTC().Format(time.RFC3339), window), kind: "deadline", priority: priority})
		}
		priority := 99
		added := false
		for _, event := range events {
			var id string
			err = tx.QueryRow(ctx, `INSERT INTO notification_events(owner_user_id,job_id,job_version,event_key,kind,priority,created_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(owner_user_id,event_key) DO NOTHING RETURNING id::text`, owner, job.ID, job.Version, event.key, event.kind, event.priority, now).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return "", err
			}
			eventIDs = append(eventIDs, id)
			priority = min(priority, event.priority)
			added = true
		}
		if !added {
			continue
		}
		item := Item{JobID: job.ID, JobVersion: job.Version, Company: job.CompanyName, Title: job.Title, Locations: []string{}, VariantKeys: []string{}, Deadline: job.DeadlineDate, NeedsConfirmation: uncertain, OfficialURL: job.ApplyURL, DetailsURL: s.origin + "/jobs/" + job.ID, Priority: priority}
		for _, variant := range variants {
			item.VariantKeys = append(item.VariantKeys, variant.Key)
			for _, place := range variant.Locations {
				if !slices.Contains(item.Locations, place.City) {
					item.Locations = append(item.Locations, place.City)
				}
			}
		}
		items = append(items, item)
	}
	sources, err := sourceStates(ctx, tx, cutoff)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	sourceData, err := json.Marshal(sources)
	if err != nil {
		return "", err
	}
	state := "queued"
	if len(items) == 0 {
		state = "empty"
	} else if !channel.Configured || !channel.Enabled {
		state = "station_only"
	}
	var profile any
	if profileID != "" {
		profile = profileID
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO notification_batches(owner_user_id,batch_key,cutoff,kind,mode,subscription_revision,channel_revision,profile_version_id,state,items,sources,created_at)
VALUES($1,$2,$3,'digest',$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id::text`, owner, key, cutoff, sub.Mode, sub.Revision, channel.Revision, profile, state, data, sourceData, now).Scan(&id)
	if err != nil {
		return "", err
	}
	if len(eventIDs) > 0 {
		if _, err = tx.Exec(ctx, "UPDATE notification_events SET batch_id=$2 WHERE id::text=ANY($1)", eventIDs, id); err != nil {
			return "", err
		}
	}
	if state == "queued" {
		if _, err = tx.Exec(ctx, "INSERT INTO notification_deliveries(batch_id,available_at,created_at,updated_at) VALUES($1,$2,$2,$2)", id, now); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE subscriptions SET last_cutoff=$2 WHERE owner_user_id=$1", owner, cutoff); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) owners(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT owner_user_id::text FROM subscriptions WHERE enabled ORDER BY owner_user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var owner string
		if err = rows.Scan(&owner); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}

// GenerateDue creates each enabled owner's daily digest after the 09:07 cutoff.
func (s *Store) GenerateDue(ctx context.Context, now time.Time) error {
	day, _, cutoff := task.DayTimes(now)
	if now.Before(cutoff) {
		return nil
	}
	owners, err := s.owners(ctx)
	if err != nil {
		return err
	}
	for _, owner := range owners {
		if _, err = s.Generate(ctx, owner, "daily:"+day, cutoff, now); err != nil && !errors.Is(err, identity.ErrForbidden) && !errors.Is(err, identity.ErrUnauthenticated) {
			return err
		}
	}
	return nil
}
