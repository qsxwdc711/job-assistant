package integration

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"jobassistant/backend/internal/automation"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/internal/task"
)

func TestDailyCollectionPlanningDomainIsolationAndRecovery(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := task.NewCollectionStore(f.pool)
	_, start, _ := task.DayTimes(time.Now())
	if err := store.Plan(ctx, start.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM scheduled_collections").Scan(&count); err != nil || count != 0 {
		t.Fatal("collection scheduled before 08:00")
	}
	for range 2 {
		if err := store.Plan(ctx, start); err != nil {
			t.Fatal(err)
		}
	}
	var enabled int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM sources WHERE enabled").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM scheduled_collections").Scan(&count); err != nil || count != enabled {
		t.Fatalf("unique source planning: %d %v", count, err)
	}
	jobs := make(chan task.Collection, 8)
	errs := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			job, err := store.Claim(ctx, start)
			if err != nil {
				errs <- err
			} else {
				jobs <- job
			}
		})
	}
	workers.Wait()
	close(jobs)
	close(errs)
	for err := range errs {
		if !errors.Is(err, identity.ErrNotFound) {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	var first task.Collection
	for job := range jobs {
		if seen[job.Domain] {
			t.Fatal("two automatic tasks claimed the same domain")
		}
		seen[job.Domain] = true
		first = job
	}
	expectedDomains := map[string]bool{}
	sources, err := catalog.NewStore(f.pool).SystemSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources {
		if source.Enabled {
			endpoint, err := url.Parse(source.OfficialURL)
			if err != nil {
				t.Fatal(err)
			}
			expectedDomains[endpoint.Hostname()] = true
		}
	}
	if len(seen) < 1 || len(seen) > len(expectedDomains) {
		t.Fatalf("claimed domains: %d", len(seen))
	}
	// Restrict recovery to one source; expired tokens cannot complete the new lease.
	if _, err := f.pool.Exec(ctx, "UPDATE scheduled_collections SET state='cancelled' WHERE id<>$1", first.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Claim(ctx, start.Add(21*time.Minute))
	if err != nil || recovered.ID != first.ID || recovered.LeaseToken == first.LeaseToken || recovered.Attempts != 2 {
		t.Fatalf("lease recovery: %#v %v", recovered, err)
	}
	if err = store.Finish(ctx, first, "succeeded", "", 1, 1, start.Add(22*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM scheduled_collections WHERE id=$1", first.ID).Scan(&state); err != nil || state != "running" {
		t.Fatal("old lease completed current run")
	}
	if err = store.Finish(ctx, recovered, "failed", "timeout_or_cancelled", 0, 0, start.Add(22*time.Minute)); err != nil {
		t.Fatal(err)
	}
	third, err := store.Claim(ctx, start.Add(27*time.Minute))
	if err != nil || third.Attempts != 3 {
		t.Fatalf("retry schedule: %#v %v", third, err)
	}
	if _, err = store.Claim(ctx, start.Add(50*time.Minute)); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("fourth attempt claimed: %v", err)
	}
	if err = f.pool.QueryRow(ctx, "SELECT state,attempts FROM scheduled_collections WHERE id=$1", first.ID).Scan(&state, &count); err != nil || state != "failed" || count != 3 {
		t.Fatalf("attempt exhaustion: %s %d %v", state, count, err)
	}
}

func TestCLIAndScheduledCollectionsShareDomainLease(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := catalog.NewStore(f.pool)
	release, err := store.CollectionLease(ctx, "tencent-campus")
	if err != nil {
		t.Fatal(err)
	}
	result := ingest.Collect(ctx, store, ingest.NewReader(), "tencent-internships")
	if result.ErrorCode != "source_busy" || result.Requests != 0 {
		t.Fatalf("concurrent source reached public HTTP: %#v", result)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	release, err = store.CollectionLease(ctx, "tencent-internships")
	if err != nil {
		t.Fatal(err)
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
}

func TestPausedCollectionPersistsCancellationOnEmptyQueue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := task.NewCollectionStore(f.pool)
	now := time.Now().UTC()
	id, err := store.Enqueue(ctx, "paused", "tencent-campus", "https://join.qq.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, "UPDATE sources SET enabled=false WHERE code='tencent-campus'"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(ctx, now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("paused source claimed: %v", err)
	}
	var state string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM scheduled_collections WHERE id=$1", id).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("paused cancellation rolled back: %s %v", state, err)
	}
}

func TestSlowCollectionDoesNotBlockDigestAndWorkerShutdown(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	collections := task.NewCollectionStore(f.pool)
	notifications := notificationStore(t, f, false)
	w := automation.New(collections, notifications, catalog.NewStore(f.pool), false)
	started := make(chan struct{})
	w.Collect = func(ctx context.Context, source string) ingest.Result {
		close(started)
		<-ctx.Done()
		return ingest.Result{SourceCode: source, Status: "failed", ErrorCode: "timeout_or_cancelled"}
	}
	now := time.Now().UTC()
	id, err := collections.Enqueue(ctx, "slow-collection", "tencent-campus", "https://join.qq.com", now)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("automation worker did not finish after cancellation")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("collection did not start")
	}
	request, err := notifications.RequestDigest(ctx, f.admin.ID, "while-collecting", now)
	if err != nil {
		t.Fatal(err)
	}
	bounded, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if err = w.Tick(bounded); err != nil {
		t.Fatalf("slow collection blocked digest: %v", err)
	}
	var state string
	if err = f.pool.QueryRow(ctx, "SELECT state FROM digest_requests WHERE id=$1", request).Scan(&state); err != nil || state != "succeeded" {
		t.Fatalf("digest stalled behind public collection: %s %v", state, err)
	}
	cancel()
	select {
	case err := <-done:
		done <- err // Cleanup owns the final join and reports worker errors.
	case <-time.After(5 * time.Second):
		t.Fatal("worker shutdown blocked on public collection")
	}
	if err = f.pool.QueryRow(context.Background(), "SELECT state FROM scheduled_collections WHERE id=$1", id).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("interrupted collection was not retained: %s %v", state, err)
	}
}
