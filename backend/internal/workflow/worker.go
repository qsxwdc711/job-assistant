// Package workflow coordinates leased private jobs; HTTP requests never invoke the model.
package workflow

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"jobassistant/backend/internal/identity"
	"jobassistant/backend/internal/llm"
	"jobassistant/backend/internal/matching"
	"jobassistant/backend/internal/profile"
	"jobassistant/backend/internal/task"
)

// Worker owns a synchronous processing loop and all of its calls inherit shutdown cancellation.
type Worker struct {
	tasks    *task.Store
	profiles *profile.Store
	matches  *matching.Store
	parser   profile.Parser
	matcher  matching.Matcher
	now      func() time.Time
}

func New(tasks *task.Store, profiles *profile.Store, matches *matching.Store, parser profile.Parser, matcher matching.Matcher) *Worker {
	return &Worker{tasks: tasks, profiles: profiles, matches: matches, parser: parser, matcher: matcher, now: func() time.Time { return time.Now().UTC() }}
}

// Run polls until ctx is cancelled. It creates no detached processing goroutines.
func (w *Worker) Run(ctx context.Context) error {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := w.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("private worker iteration failed")
		}
		if err := w.profiles.CleanupOne(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("private file deletion will retry")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

// RunOne processes at most one job and is also the integration-test boundary.
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	j, err := w.tasks.Claim(ctx, w.now())
	if errors.Is(err, identity.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	err = w.process(callCtx, j)
	code, retry, cancelled := failure(err)
	// Shutdown leaves the bounded retry available to the next worker.
	if ctx.Err() != nil {
		code = "interrupted"
		retry = true
	}
	finishCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err != nil && !retry && !cancelled && ctx.Err() == nil || err != nil && retry && j.Attempts >= j.MaxAttempts && ctx.Err() == nil {
		var markErr error
		if j.Kind == "recommend" {
			markErr = w.matches.Fail(finishCtx, j, w.matcher.Version(), code, w.now())
		} else {
			markErr = w.profiles.Fail(finishCtx, j, code, w.now())
		}
		if errors.Is(markErr, task.ErrCancelled) {
			cancelled = true
			retry = false
		} else if markErr != nil {
			return true, markErr
		}
	}
	if finishErr := w.tasks.Finish(finishCtx, j, code, retry, cancelled, w.now()); finishErr != nil {
		return true, finishErr
	}
	if cancelled && j.Kind == "recommend" {
		if err := w.matches.Retire(finishCtx, j, w.now()); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (w *Worker) process(ctx context.Context, j task.Job) error {
	switch j.Kind {
	case "extract", "parse":
		input, err := w.profiles.Prepare(ctx, j, w.now())
		if err != nil {
			return err
		}
		if input.Done {
			return nil
		}
		if j.Attempts > j.MaxAttempts {
			return llm.ErrTemporary
		}
		var draft profile.Candidate
		text := input.Text
		if j.Kind == "extract" {
			text, err = profile.Extract(ctx, input.Filename, input.Data)
			if err == nil {
				text = profile.Redact(text, input.Names)
			}
		} else {
			draft, err = w.parser.Parse(ctx, text)
		}
		if err != nil {
			return err
		}
		return w.profiles.Complete(ctx, j, text, draft, w.now())
	case "recommend":
		input, err := w.matches.Prepare(ctx, j, w.matcher.Version(), w.now())
		if err != nil {
			return err
		}
		if input.Done {
			return nil
		}
		if j.Attempts > j.MaxAttempts {
			return llm.ErrTemporary
		}
		result, err := w.matcher.Match(ctx, input.ResumeText, input.JD)
		if err != nil {
			return err
		}
		return w.matches.Complete(ctx, j, w.matcher.Version(), result, w.now())
	default:
		return task.ErrCancelled
	}
}

func failure(err error) (code string, retry, cancelled bool) {
	switch {
	case err == nil:
		return "", false, false
	case errors.Is(err, task.ErrCancelled):
		return "cancelled", false, true
	case errors.Is(err, profile.ErrInvalidFile):
		return "invalid_document", false, false
	case errors.Is(err, profile.ErrExtractorUnavailable):
		return "extractor_unavailable", false, false
	case errors.Is(err, profile.ErrUnconfigured):
		return "model_not_configured", false, false
	case errors.Is(err, profile.ErrInvalidOutput):
		return "invalid_model_output", false, false
	case errors.Is(err, llm.ErrRejected):
		return "model_rejected", false, false
	case errors.Is(err, llm.ErrTemporary), errors.Is(err, context.DeadlineExceeded):
		return "model_unavailable", true, false
	case errors.Is(err, context.Canceled):
		return "interrupted", true, false
	default:
		return "processing_failed", true, false
	}
}
