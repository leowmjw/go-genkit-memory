// Package sqlite provides an SQLite-backed persistent session store for genkit-go.
//
// It implements the [session.Store] interface from
// github.com/firebase/genkit/go/core/x/session using modernc.org/sqlite,
// a pure-Go SQLite driver that requires no CGO.
//
// SQLite is a good choice when you need SQL query capabilities over session
// state, or when deploying to environments where CGO is unavailable.
//
// # Usage
//
//	store, err := sqlite.NewStore[MyState](ctx, "sessions.db",
//	    sqlite.WithTable("my_sessions"),
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
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/firebase/genkit/go/core/x/session"
	_ "modernc.org/sqlite" // register the "sqlite" driver
)

const defaultTable = "genkit_sessions"

// Store is an SQLite-backed implementation of [session.Store].
// It persists session data to an embedded SQLite database file.
// All operations use parameterised queries and are safe for concurrent use.
type Store[S any] struct {
	db    *sql.DB
	table string
}

// Option configures a SQLite Store.
type Option func(*storeOptions)

type storeOptions struct {
	table string
}

// WithTable sets the SQLite table name used to store sessions.
// Defaults to "genkit_sessions" if not provided.
func WithTable(name string) Option {
	return func(o *storeOptions) {
		o.table = name
	}
}

// NewStore opens (or creates) an SQLite database at path and returns a Store
// ready to persist sessions of type S.
//
// The database file and session table are created if they do not exist.
// Call [Store.Close] when done to release the file handle.
//
// Use ":memory:" as path for an in-process, non-persistent SQLite database
// (useful in tests that must not touch the filesystem).
func NewStore[S any](ctx context.Context, path string, opts ...Option) (*Store[S], error) {
	o := &storeOptions{table: defaultTable}
	for _, opt := range opts {
		opt(o)
	}
	if o.table == "" {
		return nil, errors.New("sqlite.NewStore: table name must not be empty")
	}

	// Append SQLite pragmas to the DSN.
	// _busy_timeout=5000 retries locked writes for up to 5 s instead of
	// immediately returning SQLITE_BUSY.
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_busy_timeout=5000"
	} else {
		dsn = ":memory:?_busy_timeout=5000"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite.NewStore: failed to open database %q: %w", path, err)
	}

	// Limit to one writer at a time; SQLite supports concurrent readers in
	// WAL mode but only one writer per connection pool.
	db.SetMaxOpenConns(1)

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.NewStore: failed to set WAL mode: %w", err)
	}

	// Enforce foreign key constraints (good practice even if not used here).
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.NewStore: failed to enable foreign keys: %w", err)
	}

	createSQL := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    session_id  TEXT    NOT NULL PRIMARY KEY,
    state       BLOB    NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
)`, o.table)

	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.NewStore: failed to create table %q: %w", o.table, err)
	}

	return &Store[S]{db: db, table: o.table}, nil
}

// Get retrieves session data by ID.
// Returns nil, nil when the session does not exist.
func (s *Store[S]) Get(ctx context.Context, sessionID string) (*session.Data[S], error) {
	query := fmt.Sprintf(`SELECT state FROM %s WHERE session_id = ?`, s.table)
	row := s.db.QueryRowContext(ctx, query, sessionID)

	var blob []byte
	if err := row.Scan(&blob); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite.Store.Get: query failed for session %q: %w", sessionID, err)
	}

	var data session.Data[S]
	if err := json.Unmarshal(blob, &data); err != nil {
		return nil, fmt.Errorf("sqlite.Store.Get: failed to unmarshal session %q: %w", sessionID, err)
	}
	return &data, nil
}

// Save persists session data, creating or replacing the entry as needed.
func (s *Store[S]) Save(ctx context.Context, sessionID string, data *session.Data[S]) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sqlite.Store.Save: failed to marshal session %q: %w", sessionID, err)
	}

	upsert := fmt.Sprintf(`
INSERT INTO %s (session_id, state, updated_at)
    VALUES (?, ?, unixepoch())
ON CONFLICT(session_id) DO UPDATE SET
    state      = excluded.state,
    updated_at = excluded.updated_at
`, s.table)

	if _, err := s.db.ExecContext(ctx, upsert, sessionID, encoded); err != nil {
		return fmt.Errorf("sqlite.Store.Save: failed to upsert session %q: %w", sessionID, err)
	}
	return nil
}

// Delete removes a session from the store. It is a no-op if the session does
// not exist.
func (s *Store[S]) Delete(ctx context.Context, sessionID string) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE session_id = ?`, s.table)
	if _, err := s.db.ExecContext(ctx, query, sessionID); err != nil {
		return fmt.Errorf("sqlite.Store.Delete: failed to delete session %q: %w", sessionID, err)
	}
	return nil
}

// Close releases the database connection.
// It must be called when the store is no longer needed.
func (s *Store[S]) Close() error {
	return s.db.Close()
}
