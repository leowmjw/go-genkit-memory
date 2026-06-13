package sqlite_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/firebase/genkit/go/core/x/session"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

type testState struct {
	Name  string            `json:"name"`
	Count int               `json:"count"`
	Tags  map[string]string `json:"tags,omitempty"`
}

func newTestStore(t *testing.T) *sqlitestore.Store[testState] {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "sqlite-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	f.Close()

	store, err := sqlitestore.NewStore[testState](context.Background(), f.Name())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestNewStore_InMemory verifies that an in-memory SQLite store can be created.
func TestNewStore_InMemory(t *testing.T) {
	store, err := sqlitestore.NewStore[testState](context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("NewStore(:memory:) failed: %v", err)
	}
	defer store.Close()
}

// TestNewStore_CustomTable verifies that a custom table name is accepted.
func TestNewStore_CustomTable(t *testing.T) {
	store, err := sqlitestore.NewStore[testState](context.Background(), ":memory:",
		sqlitestore.WithTable("custom_table"),
	)
	if err != nil {
		t.Fatalf("NewStore with custom table failed: %v", err)
	}
	defer store.Close()
}

// TestNewStore_EmptyTableReturnsError verifies that an empty table name fails.
func TestNewStore_EmptyTableReturnsError(t *testing.T) {
	_, err := sqlitestore.NewStore[testState](context.Background(), ":memory:",
		sqlitestore.WithTable(""),
	)
	if err == nil {
		t.Fatal("expected error for empty table name")
	}
}

// TestGet_NotFound verifies that Get returns nil for a non-existent session.
func TestGet_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	data, err := store.Get(ctx, "non-existent")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if data != nil {
		t.Errorf("expected nil for non-existent session, got %v", data)
	}
}

// TestSaveAndGet_RoundTrip verifies that saved data can be retrieved intact.
func TestSaveAndGet_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	original := &session.Data[testState]{
		ID: "session-1",
		State: testState{
			Name:  "Alice",
			Count: 42,
			Tags:  map[string]string{"env": "test"},
		},
	}
	if err := store.Save(ctx, original.ID, original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := store.Get(ctx, original.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected data, got nil")
	}
	if got.ID != original.ID {
		t.Errorf("ID: want %q, got %q", original.ID, got.ID)
	}
	if got.State.Name != original.State.Name {
		t.Errorf("State.Name: want %q, got %q", original.State.Name, got.State.Name)
	}
	if got.State.Count != original.State.Count {
		t.Errorf("State.Count: want %d, got %d", original.State.Count, got.State.Count)
	}
	if got.State.Tags["env"] != "test" {
		t.Errorf("State.Tags[env]: want %q, got %q", "test", got.State.Tags["env"])
	}
}

// TestSave_OverwritesExistingSession verifies that saving again updates the record.
func TestSave_OverwritesExistingSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	first := &session.Data[testState]{ID: "s1", State: testState{Name: "v1", Count: 1}}
	if err := store.Save(ctx, first.ID, first); err != nil {
		t.Fatalf("first Save failed: %v", err)
	}

	second := &session.Data[testState]{ID: "s1", State: testState{Name: "v2", Count: 2}}
	if err := store.Save(ctx, second.ID, second); err != nil {
		t.Fatalf("second Save failed: %v", err)
	}

	got, err := store.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.State.Name != "v2" {
		t.Errorf("want %q, got %q", "v2", got.State.Name)
	}
}

// TestDelete_RemovesSession verifies that Delete makes the session unavailable.
func TestDelete_RemovesSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	d := &session.Data[testState]{ID: "del-me", State: testState{Name: "temp"}}
	if err := store.Save(ctx, d.ID, d); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := store.Delete(ctx, d.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	got, err := store.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("Get after Delete failed: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after Delete, got %v", got)
	}
}

// TestDelete_NonExistentIsNoOp verifies that deleting a missing session is safe.
func TestDelete_NonExistentIsNoOp(t *testing.T) {
	store := newTestStore(t)
	if err := store.Delete(context.Background(), "ghost"); err != nil {
		t.Fatalf("Delete of non-existent session should not error: %v", err)
	}
}

// TestPersistence verifies that data survives closing and reopening the store.
func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/persist.db"
	ctx := context.Background()

	// Write data with the first store instance.
	{
		store, err := sqlitestore.NewStore[testState](ctx, path)
		if err != nil {
			t.Fatalf("first NewStore failed: %v", err)
		}
		d := &session.Data[testState]{ID: "persist-1", State: testState{Name: "Persisted", Count: 99}}
		if err := store.Save(ctx, d.ID, d); err != nil {
			store.Close()
			t.Fatalf("Save failed: %v", err)
		}
		store.Close()
	}

	// Read data with a new store instance pointing to the same file.
	{
		store, err := sqlitestore.NewStore[testState](ctx, path)
		if err != nil {
			t.Fatalf("second NewStore failed: %v", err)
		}
		defer store.Close()

		got, err := store.Get(ctx, "persist-1")
		if err != nil {
			t.Fatalf("Get after reopen failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected persisted data, got nil")
		}
		if got.State.Name != "Persisted" {
			t.Errorf("Name: want %q, got %q", "Persisted", got.State.Name)
		}
		if got.State.Count != 99 {
			t.Errorf("Count: want %d, got %d", 99, got.State.Count)
		}
	}
}

// TestConcurrentAccess verifies that concurrent reads and writes do not corrupt data.
func TestConcurrentAccess(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const goroutines = 20
	const ops = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			key := "concurrent"
			for j := range ops {
				d := &session.Data[testState]{
					ID:    key,
					State: testState{Name: "worker", Count: id*ops + j},
				}
				if err := store.Save(ctx, key, d); err != nil {
					t.Errorf("Save error: %v", err)
				}
				if _, err := store.Get(ctx, key); err != nil {
					t.Errorf("Get error: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	got, err := store.Get(ctx, "concurrent")
	if err != nil {
		t.Fatalf("final Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected data after concurrent writes, got nil")
	}
}

// TestSessionIntegration exercises the full genkit session lifecycle on top of
// the SQLite store.
func TestSessionIntegration(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess, err := session.New(ctx,
		session.WithID[testState]("integ-1"),
		session.WithInitialState(testState{Name: "Start", Count: 0}),
		session.WithStore(store),
	)
	if err != nil {
		t.Fatalf("session.New failed: %v", err)
	}

	for i := 1; i <= 5; i++ {
		state := sess.State()
		state.Count = i
		state.Name = "Turn"
		if err := sess.UpdateState(ctx, state); err != nil {
			t.Fatalf("UpdateState at turn %d failed: %v", i, err)
		}
	}

	loaded, err := session.Load(ctx, store, "integ-1")
	if err != nil {
		t.Fatalf("session.Load failed: %v", err)
	}

	s := loaded.State()
	if s.Name != "Turn" {
		t.Errorf("Name: want %q, got %q", "Turn", s.Name)
	}
	if s.Count != 5 {
		t.Errorf("Count: want %d, got %d", 5, s.Count)
	}
}

// TestLoad_NotFound verifies that loading a non-existent session returns
// a NotFoundError.
func TestLoad_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := session.Load(ctx, store, "does-not-exist")
	if err == nil {
		t.Fatal("expected NotFoundError, got nil")
	}

	var notFound *session.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("expected *session.NotFoundError, got %T: %v", err, err)
	}
}
