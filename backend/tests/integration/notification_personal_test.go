package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/notification"
	"jobassistant/backend/internal/profile"
)

func TestPersonalNotificationsRequireCurrentPermissionProfileAndRecommendation(t *testing.T) {
	for _, mode := range []string{"no_recommendation", "vip_revoked", "vip_expired", "profile_changed", "job_changed", "model_changed"} {
		t.Run(mode, func(t *testing.T) {
			f := newS3Fixture(t)
			ctx := context.Background()
			detail := f.upload(t)
			f.confirm(t, detail)
			job := f.job(t)
			if mode != "no_recommendation" {
				if mode == "model_changed" {
					f.recommend(t, job)
					f.run(t)
				} else {
					if _, err := f.matches.Generate(ctx, f.scope, []string{job.ID}, false, false, false, f.model.Version(), "test", time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			store, err := notification.NewStore(f.pool, notification.Options{Key: bytes.Repeat([]byte{4}, 32), Origin: "http://app.test", ModelVersion: f.model.Version(), SendEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(time.Second)
			subscribe(t, store, f.vip, "", hookA, now)
			if _, err = store.SaveSubscription(ctx, notification.Scope{ActorID: f.vip.ID}, notification.Subscription{Mode: "personalized", Enabled: true, Revision: 1}, "test", now); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Generate(ctx, f.vip.ID, "personal", now, now); err != nil {
				t.Fatal(err)
			}
			list, err := store.List(ctx, notification.Scope{ActorID: f.vip.ID}, 20, 0, now)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "no_recommendation" {
				if len(list[0].Items) != 0 || list[0].State != "empty" {
					t.Fatal("personalized mode fell back to broad filtering")
				}
				return
			}
			if len(list[0].Items) != 1 {
				t.Fatalf("valid direction not isolated: %d", len(list[0].Items))
			}
			switch mode {
			case "vip_revoked":
				_, err = f.service.ChangeRole(ctx, f.admin, f.vip.ID, identity.RoleStandard, nil, "test")
			case "vip_expired":
				expiry := now.Add(-500 * time.Millisecond)
				_, err = f.service.ChangeRole(ctx, f.admin, f.vip.ID, identity.RoleVIP, &expiry, "test")
			case "profile_changed":
				fresh, e := f.profiles.Detail(ctx, f.scope, detail.Resume.ID, "test", time.Now())
				if e != nil {
					t.Fatal(e)
				}
				_, err = f.profiles.Confirm(ctx, f.scope, detail.Resume.ID, fresh.Resume.Revision, profile.Candidate{Education: "master"}, false, "test", time.Now())
			case "job_changed":
				doc := job.Document
				doc.Variants[0].Requirements = "明确变更的岗位条件"
				publishEvidence(t, f.fixture, doc)
			case "model_changed":
				store, err = notification.NewStore(f.pool, notification.Options{Key: bytes.Repeat([]byte{4}, 32), Origin: "http://app.test", ModelVersion: "different-model-version", SendEnabled: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			// No sender is needed: every stale/unauthorized branch must cancel before HTTP.
			if _, err = store.DispatchOne(ctx, nil, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			var state string
			if err = f.pool.QueryRow(ctx, "SELECT state FROM notification_deliveries LIMIT 1").Scan(&state); err != nil || state != "cancelled" {
				t.Fatalf("stale personal notification sent: %s %v", state, err)
			}
		})
	}
}

func TestSnapshotFiltersKeepCoherentVariants(t *testing.T) {
	job := catalog.Job{Document: evidenceJob(), Published: true}
	for _, query := range []struct {
		city string
		want int
	}{{"深圳市", 1}, {"合肥市", 0}} {
		variants := catalog.MatchingVariants(job, catalog.Filter{Directions: []string{"backend"}, Cities: []string{query.city}, IncludeUnknown: true}, time.Now())
		if len(variants) != query.want {
			t.Fatalf("mixed backend/%s accepted", query.city)
		}
	}
}

func TestPersonalNotificationIncludesRecommendationReadyAfterEmptyDigest(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	detail := f.upload(t)
	f.confirm(t, detail)
	job := f.job(t)
	store := notificationStore(t, f.fixture, false)
	now := time.Now().UTC().Add(time.Second)
	subscribe(t, store, f.vip, "", hookA, now)
	scope := notification.Scope{ActorID: f.vip.ID}
	if _, err := store.SaveSubscription(ctx, scope, notification.Subscription{Mode: "personalized", Enabled: true, Revision: 1}, "test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Generate(ctx, f.vip.ID, "before-recommendation", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.matches.Generate(ctx, f.scope, []string{job.ID}, false, false, false, f.model.Version(), "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	next := now.Add(time.Hour)
	if _, err := store.Generate(ctx, f.vip.ID, "after-recommendation", next, next); err != nil {
		t.Fatal(err)
	}
	batches, err := store.List(ctx, scope, 20, 0, next)
	if err != nil || len(batches) != 2 || len(batches[0].Items) != 1 || batches[1].State != "empty" {
		t.Fatalf("newly available recommendation lost: %#v %v", batches, err)
	}
	if _, err := store.Generate(ctx, f.vip.ID, "no-repeat", next.Add(time.Minute), next.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_events WHERE owner_user_id=$1", f.vip.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("repeated matching event: %d %v", events, err)
	}
}

func TestDeadlineWindowDeduplicatesAcrossSubscriptionModes(t *testing.T) {
	f := newS3Fixture(t)
	ctx := context.Background()
	f.confirm(t, f.upload(t))
	job := f.job(t)
	now := time.Now().UTC().Add(time.Second)
	deadline := now.Add(2 * 24 * time.Hour)
	job.DeadlineAt = &deadline
	job.DeadlineDate = deadline.Format("2006-01-02")
	catalogStore := publishEvidence(t, f.fixture, job.Document)
	page, err := catalogStore.Jobs(ctx, f.vip.ID, catalog.Filter{Limit: 20, IncludeUnknown: true}, now)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("deadline job: %v", err)
	}
	store := notificationStore(t, f.fixture, false)
	subscribe(t, store, f.vip, "", hookA, now)
	if _, err = store.Generate(ctx, f.vip.ID, "filtered", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = f.matches.Generate(ctx, f.scope, []string{page.Items[0].ID}, false, false, false, f.model.Version(), "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SaveSubscription(ctx, notification.Scope{ActorID: f.vip.ID}, notification.Subscription{Mode: "personalized", Enabled: true, Revision: 1}, "test", now); err != nil {
		t.Fatal(err)
	}
	next := now.Add(time.Minute)
	if _, err = store.Generate(ctx, f.vip.ID, "personalized", next, next); err != nil {
		t.Fatal(err)
	}
	var reminders int
	if err = f.pool.QueryRow(ctx, "SELECT count(*) FROM notification_events WHERE owner_user_id=$1 AND kind='deadline'", f.vip.ID).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatalf("subscription change repeated the same deadline window: %d %v", reminders, err)
	}
}
