// Package bbolt provides a BBolt-backed persistent session store for genkit-go.
//
// It implements the [session.Store] interface from
// github.com/firebase/genkit/go/core/x/session, enabling sessions to survive
// process restarts using an embedded key-value database.
//
// BBolt stores all data in a single file, making it well-suited for
// single-node deployments, offline scenarios, or testing without external
// infrastructure.
//
// # Usage
//
//	store, err := bbolt.NewStore[MyState](ctx, "sessions.db",
//	    bbolt.WithBucket("my-sessions"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer store.Close()
//
//	sess, err := session.New(ctx,
//	    session.WithStore(store),
//	    session.WithInitialState(MyState{...}),
//	)
package bbolt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/firebase/genkit/go/core/x/session"
	bolt "go.etcd.io/bbolt"
)

const defaultBucket = "genkit-sessions"

// Store is a BBolt-backed implementation of [session.Store].
// It persists session data to an embedded key-value database file.
// All operations are safe for concurrent use.
type Store[S any] struct {
	db     *bolt.DB
	bucket []byte
}

// Option configures a BBolt Store.
type Option func(*storeOptions)

type storeOptions struct {
	bucket string
}

// WithBucket sets the BBolt bucket name used to store sessions.
// Defaults to "genkit-sessions" if not provided.
func WithBucket(name string) Option {
	return func(o *storeOptions) {
		o.bucket = name
	}
}

// NewStore opens (or creates) a BBolt database at path and returns a Store
// ready to persist sessions of type S.
//
// The database file is created if it does not exist.
// Call [Store.Close] when done to release the file lock.
func NewStore[S any](_ context.Context, path string, opts ...Option) (*Store[S], error) {
	o := &storeOptions{bucket: defaultBucket}
	for _, opt := range opts {
		opt(o)
	}
	if o.bucket == "" {
		return nil, errors.New("bbolt.NewStore: bucket name must not be empty")
	}

	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("bbolt.NewStore: failed to open database %q: %w", path, err)
	}

	bucketName := []byte(o.bucket)

	// Ensure the bucket exists.
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucketName)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bbolt.NewStore: failed to create bucket %q: %w", o.bucket, err)
	}

	return &Store[S]{db: db, bucket: bucketName}, nil
}

// Get retrieves session data by ID.
// Returns nil, nil when the session does not exist.
func (s *Store[S]) Get(_ context.Context, sessionID string) (*session.Data[S], error) {
	var data *session.Data[S]

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(s.bucket)
		if b == nil {
			return nil
		}
		v := b.Get([]byte(sessionID))
		if v == nil {
			return nil
		}
		var d session.Data[S]
		if err := json.Unmarshal(v, &d); err != nil {
			return fmt.Errorf("bbolt.Store.Get: failed to unmarshal session %q: %w", sessionID, err)
		}
		data = &d
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Save persists session data, creating or updating the entry as needed.
func (s *Store[S]) Save(_ context.Context, sessionID string, data *session.Data[S]) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("bbolt.Store.Save: failed to marshal session %q: %w", sessionID, err)
	}

	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(s.bucket)
		if b == nil {
			return fmt.Errorf("bbolt.Store.Save: bucket %q not found", string(s.bucket))
		}
		if err := b.Put([]byte(sessionID), encoded); err != nil {
			return fmt.Errorf("bbolt.Store.Save: failed to put session %q: %w", sessionID, err)
		}
		return nil
	})
}

// Delete removes a session from the store. It is a no-op if the session does
// not exist.
func (s *Store[S]) Delete(_ context.Context, sessionID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(s.bucket)
		if b == nil {
			return nil
		}
		if err := b.Delete([]byte(sessionID)); err != nil {
			return fmt.Errorf("bbolt.Store.Delete: failed to delete session %q: %w", sessionID, err)
		}
		return nil
	})
}

// Close releases the BBolt file lock and flushes any pending writes.
// It must be called when the store is no longer needed.
func (s *Store[S]) Close() error {
	return s.db.Close()
}
