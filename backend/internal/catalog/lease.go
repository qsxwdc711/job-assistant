package catalog

import (
	"context"
	"errors"
	"net/url"
	"time"
)

// ErrCollectionBusy prevents automatic and CLI readers from overlapping a host.
var ErrCollectionBusy = errors.New("collection domain or global slots are busy")

// CollectionLease holds one pooled connection for the lifetime of a bounded read.
// All collection entrypoints share the domain lock and three global slots.
func (s *Store) CollectionLease(ctx context.Context, source string) (func() error, error) {
	var raw string
	if err := s.pool.QueryRow(ctx, "SELECT official_url FROM sources WHERE code=$1", source).Scan(&raw); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("invalid collection domain")
	}
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := connection.Exec(cleanup, "SELECT pg_advisory_unlock_all()")
		if err != nil {
			if closeErr := connection.Conn().Close(cleanup); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
		}
		connection.Release()
		return err
	}
	var locked bool
	err = connection.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,73428403))", u.Hostname()).Scan(&locked)
	if err != nil || !locked {
		cleanupErr := release()
		if err == nil {
			err = ErrCollectionBusy
		}
		return nil, errors.Join(err, cleanupErr)
	}
	for slot := 1; slot <= 3; slot++ {
		if err = connection.QueryRow(ctx, "SELECT pg_try_advisory_lock(73428404,$1)", slot).Scan(&locked); err != nil {
			break
		}
		if locked {
			return release, nil
		}
	}
	cleanupErr := release()
	if err == nil {
		err = ErrCollectionBusy
	}
	return nil, errors.Join(err, cleanupErr)
}
