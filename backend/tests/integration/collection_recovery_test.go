package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"jobassistant/backend/internal/automation"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/internal/task"
)

func recoveryFixture(t *testing.T, source string) (*fixture, *task.CollectionStore, time.Time) {
	t.Helper()
	f := newStoreFixture(t)
	if _, err := f.pool.Exec(context.Background(), "UPDATE sources SET enabled=(code=$1)", source); err != nil {
		t.Fatal(err)
	}
	return f, task.NewCollectionStore(f.pool), time.Date(2026, 10, 19, 8, 0, 0, 0, task.Shanghai)
}

func exhaustCollection(t *testing.T, queue *task.CollectionStore, start time.Time, code string) (task.Collection, time.Time) {
	t.Helper()
	var first task.Collection
	for i, delay := range []time.Duration{0, time.Minute, 5 * time.Minute} {
		now := start.Add(delay)
		job, err := queue.Claim(context.Background(), now)
		if err != nil || job.Attempts != i+1 || (i > 0 && job.ID != first.ID) {
			t.Fatalf("attempt %d: %#v %v", i+1, job, err)
		}
		if i == 0 {
			first = job
		}
		if err := queue.Finish(context.Background(), job, "failed", code, 0, 0, now); err != nil {
			t.Fatal(err)
		}
	}
	return first, start.Add(5 * time.Minute)
}

func collectionCount(t *testing.T, f *fixture, want int) {
	t.Helper()
	var got int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM scheduled_collections").Scan(&got); err != nil || got != want {
		t.Fatalf("task count: got %d want %d (%v)", got, want, err)
	}
}

func planCollections(t *testing.T, queue *task.CollectionStore, now time.Time) {
	t.Helper()
	if err := queue.Plan(context.Background(), now); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionRecoveryBoundedRoundsConcurrentPlanningAndHistory(t *testing.T) {
	f, queue, start := recoveryFixture(t, "tencent-campus")
	ctx := context.Background()
	planCollections(t, queue, start.Add(-time.Second))
	collectionCount(t, f, 0)
	planCollections(t, queue, start)
	original, failed := exhaustCollection(t, queue, start, "network_error")
	planCollections(t, queue, failed.Add(30*time.Minute-time.Second))
	collectionCount(t, f, 1)
	firstDue := failed.Add(30 * time.Minute)
	var group sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		group.Go(func() { errs <- queue.Plan(ctx, firstDue) })
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	collectionCount(t, f, 2)
	roundOne, failedOne := exhaustCollection(t, queue, firstDue, "network_error")
	if roundOne.ID == original.ID {
		t.Fatal("recovery reset the original task")
	}
	planCollections(t, queue, failedOne.Add(2*time.Hour-time.Second))
	collectionCount(t, f, 2)
	planCollections(t, queue, failedOne.Add(2*time.Hour))
	roundTwo, _ := exhaustCollection(t, queue, failedOne.Add(2*time.Hour), "network_error")
	planCollections(t, queue, start.Add(13*time.Hour))
	collectionCount(t, f, 3)
	var state, code, parent string
	var attempts, round, total int
	if err := f.pool.QueryRow(ctx, "SELECT state,error_code,attempts FROM scheduled_collections WHERE id=$1", original.ID).Scan(&state, &code, &attempts); err != nil || state != "failed" || code != "network_error" || attempts != 3 {
		t.Fatalf("original failure changed: %s %s %d %v", state, code, attempts, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT recovery_parent_id::text,recovery_round FROM scheduled_collections WHERE id=$1", roundTwo.ID).Scan(&parent, &round); err != nil || parent != roundOne.ID || round != 2 {
		t.Fatalf("recovery ancestry: %s %d %v", parent, round, err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT sum(attempts) FROM scheduled_collections").Scan(&total); err != nil || total != 9 {
		t.Fatalf("daily budget: %d %v", total, err)
	}
}

func TestCollectionRecoveryErrorClassification(t *testing.T) {
	for _, code := range []string{"network_error", "timeout", "timeout_or_cancelled", "source_busy", "interrupted", "restricted", "schema_changed", "invalid_response", "publication_failed", "attempts_exhausted", "login_required"} {
		t.Run(code, func(t *testing.T) {
			f, queue, start := recoveryFixture(t, "tencent-campus")
			ctx := context.Background()
			planCollections(t, queue, start)
			// Persist the terminal state, including error classes that do not retry in the first place.
			if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET state='failed',attempts=3,error_code=$1,updated_at=$2", code, start); err != nil {
				t.Fatal(err)
			}
			planCollections(t, queue, start.Add(time.Hour))
			want := 1
			if code == "network_error" || code == "timeout" || code == "timeout_or_cancelled" || code == "source_busy" || code == "interrupted" {
				want = 2
			}
			collectionCount(t, f, want)
		})
	}
}

func TestCollectionRecoveryManualAndCLISuccessAndNewRestrictions(t *testing.T) {
	for _, mode := range []string{"manual", "cli", "restricted", "invalid_response", "paused", "invalid_parent", "wrong_day_key"} {
		t.Run(mode, func(t *testing.T) {
			f, queue, start := recoveryFixture(t, "tencent-campus")
			ctx := context.Background()
			planCollections(t, queue, start)
			original, failed := exhaustCollection(t, queue, start, "network_error")
			due := failed.Add(30 * time.Minute)
			planCollections(t, queue, due)
			collectionCount(t, f, 2)
			now := due.Add(time.Second)
			switch mode {
			case "manual":
				id, err := queue.Enqueue(ctx, "manual:recovered", "tencent-campus", "https://join.qq.com", now)
				if err != nil {
					t.Fatal(err)
				}
				job, err := queue.Claim(ctx, now)
				if err != nil || job.ID != id {
					t.Fatalf("recovery overtook manual work: %#v %v", job, err)
				}
				if _, err = queue.Claim(ctx, now); !errors.Is(err, identity.ErrNotFound) {
					t.Fatal("recovery ran alongside manual task", err)
				}
				if err = queue.Finish(ctx, job, "succeeded", "", 1, 1, now); err != nil {
					t.Fatal(err)
				}
			case "cli":
				store := catalog.NewStore(f.pool)
				run, err := store.BeginRun(ctx, "tencent-campus", now)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.Publish(ctx, run, []catalog.Document{evidenceJob()}, 1, nil, now); err != nil {
					t.Fatal(err)
				}
			case "restricted", "invalid_response":
				store := catalog.NewStore(f.pool)
				run, err := store.BeginRun(ctx, "tencent-campus", now)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.FailRun(ctx, run, mode, mode == "restricted", now); err != nil {
					t.Fatal(err)
				}
			case "paused":
				if _, err := f.pool.Exec(ctx, "UPDATE sources SET enabled=false"); err != nil {
					t.Fatal(err)
				}
			case "invalid_parent":
				if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET state='cancelled' WHERE id=$1", original.ID); err != nil {
					t.Fatal(err)
				}
			case "wrong_day_key":
				if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET schedule_key='recovery:2026-10-18:tencent-campus:1' WHERE recovery_round=1"); err != nil {
					t.Fatal(err)
				}
			}
			if job, err := queue.Claim(ctx, now); !errors.Is(err, identity.ErrNotFound) {
				t.Fatalf("ineligible recovery claimed: %#v %v", job, err)
			}
			var state string
			if err := f.pool.QueryRow(ctx, "SELECT state FROM scheduled_collections WHERE recovery_round=1").Scan(&state); err != nil || state != "cancelled" {
				t.Fatalf("ineligible recovery retained: %s %v", state, err)
			}
			planCollections(t, queue, now.Add(3*time.Hour))
			want := 2
			if mode == "manual" {
				want = 3
			}
			collectionCount(t, f, want)
		})
	}
}

func TestCollectionRecoveryBoundariesAndRuntimeFilters(t *testing.T) {
	f, queue, start := recoveryFixture(t, "zjdit-careers")
	ctx := context.Background()
	planCollections(t, queue, start)
	_, failed := exhaustCollection(t, queue, start, "network_error")
	if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET updated_at=$1", start.Add(13*time.Hour+30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, start.Add(14*time.Hour))
	collectionCount(t, f, 1)
	if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET updated_at=$1", failed); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, start.Add(14*time.Hour-time.Second))
	collectionCount(t, f, 2)
	container := queue.WithSources(nil, []string{"zjdit-careers"})
	if _, err := container.Claim(ctx, start.Add(15*time.Hour)); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal("container claimed host-only recovery", err)
	}
	host := queue.WithSources([]string{"zjdit-careers"}, nil)
	job, err := host.Claim(ctx, start.Add(15*time.Hour+59*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Claim(ctx, start.Add(16*time.Hour+time.Minute)); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal(err)
	}
	var state string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM scheduled_collections WHERE id=$1", job.ID).Scan(&state); err != nil || state != "running" {
		t.Fatal("midnight cancelled an active lease", err)
	}
	if _, err = host.Claim(ctx, start.Add(17*time.Hour)); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT state FROM scheduled_collections WHERE id=$1", job.ID).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("expired previous-day recovery survived", err)
	}
	planCollections(t, queue, start.Add(24*time.Hour))
	collectionCount(t, f, 3)
}

func TestCollectionRecoveryDoesNotPlanPrematureOrDisabledWork(t *testing.T) {
	for _, mode := range []string{"attempts_remaining", "lease_exhausted", "manual_failure", "already_refreshed", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			f, queue, start := recoveryFixture(t, "tencent-campus")
			ctx := context.Background()
			planCollections(t, queue, start)
			original, failed := exhaustCollection(t, queue, start, "network_error")
			switch mode {
			case "attempts_remaining":
				_, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET attempts=2 WHERE id=$1", original.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "lease_exhausted":
				_, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET state='running',lease_until=$2 WHERE id=$1", original.ID, failed)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := queue.Claim(ctx, failed); !errors.Is(err, identity.ErrNotFound) {
					t.Fatal(err)
				}
			case "manual_failure":
				_, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET schedule_key='manual:not-automatic' WHERE id=$1", original.ID)
				if err != nil {
					t.Fatal(err)
				}
			case "already_refreshed":
				_, err := f.pool.Exec(ctx, "UPDATE sources SET last_status='succeeded',last_succeeded_at=$1,last_checked_at=$1", failed.Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
			case "disabled":
				w := automation.New(queue, notificationStore(t, f, false), catalog.NewStore(f.pool), false)
				w.Now = func() time.Time { return failed.Add(time.Hour) }
				if err := w.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				collectionCount(t, f, 1)
				return
			}
			planCollections(t, queue, failed.Add(time.Hour))
			want := 1
			if mode == "manual_failure" {
				want = 2
			} // Today's daily key is still independently planned.
			collectionCount(t, f, want)
		})
	}
}

func TestCollectionRecoveryWorkerPublishesOnceAndKeepsNotificationsIndependent(t *testing.T) {
	f, queue, start := recoveryFixture(t, "tencent-campus")
	ctx := context.Background()
	store := catalog.NewStore(f.pool)
	run, err := store.BeginRun(ctx, "tencent-campus", start.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(ctx, run, []catalog.Document{evidenceJob()}, 1, nil, start.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, start)
	_, failed := exhaustCollection(t, queue, start, "network_error")
	due := failed.Add(30 * time.Minute)
	planCollections(t, queue, due)
	w := automation.New(queue, notificationStore(t, f, false), store, true)
	w.Now = func() time.Time { return due }
	calls := 0
	w.Collect = func(ctx context.Context, source string) ingest.Result {
		calls++
		id, err := store.BeginRun(ctx, source, due)
		if err == nil {
			err = store.Publish(ctx, id, []catalog.Document{evidenceJob()}, 1, nil, due)
		}
		if err != nil {
			return ingest.Result{Status: "failed", ErrorCode: fmt.Sprint(err)}
		}
		return ingest.Result{Status: "succeeded", Fetched: 1, Published: 1}
	}
	if done, err := w.CollectOne(ctx); err != nil || !done {
		t.Fatal(done, err)
	}
	if err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if done, err := w.CollectOne(ctx); err != nil || done {
		t.Fatal(done, err)
	}
	var versions, batches, deliveries int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM job_versions),(SELECT count(*) FROM notification_batches),(SELECT count(*) FROM notification_deliveries)").Scan(&versions, &batches, &deliveries); err != nil || versions != 1 || calls != 1 || batches != 0 || deliveries != 0 {
		t.Fatalf("recovery side effects versions=%d calls=%d batches=%d deliveries=%d %v", versions, calls, batches, deliveries, err)
	}
}

func TestCollectionRecoveryRechecksPausedSourceAfterWaitingForLock(t *testing.T) {
	f, queue, start := recoveryFixture(t, "tencent-campus")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	planCollections(t, queue, start)
	_, failed := exhaustCollection(t, queue, start, "network_error")
	due := failed.Add(30 * time.Minute)
	planCollections(t, queue, due)
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer task.Rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT code FROM sources WHERE code='tencent-campus' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := queue.Claim(ctx, due); done <- err }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("claim did not stop")
		}
	})
	// Observe the real lock wait, so the pause happens after candidate selection.
	for {
		var waiting bool
		if err = f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query='SELECT code FROM sources WHERE code=$1 FOR UPDATE')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("claim returned before source lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE sources SET enabled=false WHERE code='tencent-campus'"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, identity.ErrNotFound) {
			t.Fatalf("paused recovery was claimed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var state, code string
	if err = f.pool.QueryRow(ctx, "SELECT state,error_code FROM scheduled_collections WHERE recovery_round=1").Scan(&state, &code); err != nil || state != "cancelled" || code != "source_paused" {
		t.Fatalf("pause race persisted %s %s %v", state, code, err)
	}
}

func TestCollectionRecoveryRejectsBrokenAncestryAndDuplicateChildren(t *testing.T) {
	f, queue, start := recoveryFixture(t, "tencent-campus")
	ctx := context.Background()
	planCollections(t, queue, start)
	original, failed := exhaustCollection(t, queue, start, "network_error")
	due := failed.Add(30 * time.Minute)
	planCollections(t, queue, due)
	if _, err := f.pool.Exec(ctx, `INSERT INTO scheduled_collections(schedule_key,source_code,domain,available_at,created_at,updated_at,recovery_parent_id,recovery_round)
VALUES('different-key','tencent-campus','join.qq.com',$1,$1,$1,$2,1)`, due, original.ID); err == nil {
		t.Fatal("database allowed two children of one failed task")
	}
	_, roundFailed := exhaustCollection(t, queue, due, "network_error")
	if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET state='cancelled' WHERE id=$1", original.ID); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, roundFailed.Add(2*time.Hour))
	collectionCount(t, f, 2)
}

func TestCollectionRecoveryConcurrentClaimAndFailureKeepPublishedContent(t *testing.T) {
	f, queue, start := recoveryFixture(t, "tencent-campus")
	ctx := context.Background()
	store := catalog.NewStore(f.pool)
	run, err := store.BeginRun(ctx, "tencent-campus", start.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(ctx, run, []catalog.Document{evidenceJob()}, 1, nil, start.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, start)
	_, failed := exhaustCollection(t, queue, start, "network_error")
	due := failed.Add(30 * time.Minute)
	planCollections(t, queue, due)
	var group sync.WaitGroup
	jobs := make(chan task.Collection, 8)
	errs := make(chan error, 8)
	for range 8 {
		group.Go(func() {
			job, err := queue.Claim(ctx, due)
			if err == nil {
				jobs <- job
			} else {
				errs <- err
			}
		})
	}
	group.Wait()
	close(jobs)
	close(errs)
	for err := range errs {
		if !errors.Is(err, identity.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if len(jobs) != 1 {
		t.Fatalf("concurrent recovery claims=%d", len(jobs))
	}
	job := <-jobs
	if err := queue.Finish(ctx, job, "failed", "schema_changed", 123, 0, due); err != nil {
		t.Fatal(err)
	}
	planCollections(t, queue, due.Add(3*time.Hour))
	collectionCount(t, f, 2)
	var jobsCount, versions int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM jobs WHERE published),(SELECT count(*) FROM job_versions)").Scan(&jobsCount, &versions); err != nil || jobsCount != 1 || versions != 1 {
		t.Fatalf("failed recovery changed content %d %d %v", jobsCount, versions, err)
	}
}
