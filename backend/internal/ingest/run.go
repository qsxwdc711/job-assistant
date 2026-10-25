package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"jobassistant/backend/internal/catalog"
)

// Result is the public operational outcome of one complete source attempt.
type Result struct {
	SourceCode string `json:"source_code"`
	Status     string `json:"status"`
	Fetched    int    `json:"fetched"`
	Published  int    `json:"published"`
	ErrorCode  string `json:"error_code,omitempty"`
	Requests   int    `json:"requests"`
}

// Collect fetches a bounded complete source snapshot and publishes only after full validation.
func Collect(ctx context.Context, store *catalog.Store, reader *Reader, source string) Result {
	defer reader.Client.CloseIdleConnections()
	result := Result{SourceCode: source, Status: "failed"}
	release, err := store.CollectionLease(ctx, source)
	if err != nil {
		result.ErrorCode = "source_unavailable"
		if errors.Is(err, catalog.ErrCollectionBusy) {
			result.ErrorCode = "source_busy"
		}
		return result
	}
	defer func() {
		if err := release(); err != nil {
			slog.Warn("collection lease cleanup failed")
		}
	}()
	run, err := store.BeginRun(ctx, source, time.Now().UTC())
	if err != nil {
		result.ErrorCode = "source_unavailable"
		return result
	}
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	documents, fetched, err := reader.ReadSource(readCtx, source)
	result.Fetched = fetched
	result.Requests = reader.Requests
	if err == nil {
		err = store.Publish(readCtx, run, documents, fetched, reader.Evidence, time.Now().UTC(), reader.Exclusions...)
		if err == nil {
			result.Status = "succeeded"
			result.Published = len(documents)
			return result
		}
		result.ErrorCode = "publication_failed"
	} else {
		result.ErrorCode = "network_error"
		if errors.Is(err, ErrRestricted) {
			result.ErrorCode = "restricted"
			result.Status = "restricted"
		}
		if errors.Is(err, ErrSchema) {
			result.ErrorCode = "schema_changed"
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			result.ErrorCode = "timeout_or_cancelled"
		}
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	if saveErr := store.FailRun(cleanupCtx, run, result.ErrorCode, result.Status == "restricted", time.Now().UTC()); saveErr != nil {
		result.ErrorCode += "_not_recorded"
	}
	return result
}
