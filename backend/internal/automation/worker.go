// Package automation coordinates public collection and public-only notification workers.
package automation

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
	"jobassistant/backend/internal/catalog"
	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/ingest"
	"jobassistant/backend/internal/notification"
	"jobassistant/backend/internal/task"
)

// Worker runs independent bounded collection, digest and delivery loops.
type Worker struct {
	Collections     *task.CollectionStore
	Notifications   *notification.Store
	Sender          *notification.Sender
	Collect         func(context.Context, string) ingest.Result
	Now             func() time.Time
	ScheduleEnabled bool
}

// New wires real source readers and a bounded sender; Run owns their loop lifetime.
func New(collections *task.CollectionStore, notifications *notification.Store, catalogStore *catalog.Store, scheduleEnabled bool) *Worker {
	return &Worker{Collections: collections, Notifications: notifications, Sender: notification.NewSender(nil), Collect: func(ctx context.Context, source string) ingest.Result {
		return ingest.Collect(ctx, catalogStore, ingest.NewReader(), source)
	}, Now: func() time.Time { return time.Now().UTC() }, ScheduleEnabled: scheduleEnabled}
}

// CollectOne claims one persistent source task and retains interrupted work for retry.
func (w *Worker) CollectOne(ctx context.Context) (bool, error) {
	job, err := w.Collections.Claim(ctx, w.Now())
	if errors.Is(err, identity.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result := ingest.Result{Status: "failed", ErrorCode: "attempts_exhausted"}
	if job.Attempts <= 3 {
		callCtx, cancel := context.WithTimeout(ctx, 17*time.Minute)
		result = w.Collect(callCtx, job.SourceCode)
		cancel()
	}
	if ctx.Err() != nil {
		result.Status, result.ErrorCode = "failed", "interrupted"
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return true, w.Collections.Finish(cleanup, job, result.Status, result.ErrorCode, result.Fetched, result.Published, w.Now())
}

// Tick plans due daily work and processes manual requests independently of collection.
func (w *Worker) Tick(ctx context.Context) error {
	if w.ScheduleEnabled {
		if err := w.Collections.Plan(ctx, w.Now()); err != nil {
			return err
		}
		if err := w.Notifications.GenerateDue(ctx, w.Now()); err != nil {
			return err
		}
	}
	_, err := w.Notifications.ProcessRequest(ctx, w.Now())
	return err
}
func loop(ctx context.Context, delay time.Duration, run func(context.Context) error) error {
	timer := time.NewTicker(delay)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := run(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("automation iteration failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// Run blocks until cancellation and joins all loops before returning.
func (w *Worker) Run(ctx context.Context) error {
	group, runCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return loop(runCtx, 30*time.Second, w.Tick) })
	for range 3 {
		group.Go(func() error {
			return loop(runCtx, time.Second, func(ctx context.Context) error { _, err := w.CollectOne(ctx); return err })
		})
	}
	group.Go(func() error {
		return loop(runCtx, time.Second, func(ctx context.Context) error {
			bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, err := w.Notifications.DispatchOne(bounded, w.Sender, w.Now())
			return err
		})
	})
	return group.Wait()
}
