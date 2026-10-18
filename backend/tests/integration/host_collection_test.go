package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/task"
)

func TestPublicCollectionRuntimeSourceFiltersShareRecovery(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC)
	queue := task.NewCollectionStore(f.pool)
	if err := queue.Plan(ctx, now); err != nil {
		t.Fatal(err)
	}
	allow := []string{"zjdit-careers"}
	host := queue.WithSources(allow, nil)
	allow[0] = "tencent-campus"
	container := queue.WithSources(nil, []string{"zjdit-careers"})
	job, err := host.Claim(ctx, now)
	if err != nil || job.SourceCode != "zjdit-careers" {
		t.Fatalf("host source scope: %#v %v", job, err)
	}
	other, err := container.Claim(ctx, now)
	if err != nil || other.SourceCode == "zjdit-careers" {
		t.Fatalf("container source scope: %#v %v", other, err)
	}
	if _, err = host.Claim(ctx, now); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("host claimed an unrelated or running source: %v", err)
	}
	if err = host.Finish(ctx, job, "failed", "interrupted", 0, 0, now); err != nil {
		t.Fatal(err)
	}
	retry, err := host.Claim(ctx, now.Add(time.Minute))
	if err != nil || retry.ID != job.ID || retry.Attempts != 2 || retry.LeaseToken == job.LeaseToken {
		t.Fatalf("runtime interruption recovery: %#v %v", retry, err)
	}
	if err = host.Finish(ctx, retry, "succeeded", "", 26, 12, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var state string
	var published int
	if err = f.pool.QueryRow(ctx, "SELECT state,published FROM scheduled_collections WHERE id=$1", job.ID).Scan(&state, &published); err != nil || state != "succeeded" || published != 12 {
		t.Fatalf("shared queue result: %s %d %v", state, published, err)
	}
}
