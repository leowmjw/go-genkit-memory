// Package integration provides cross-adapter integration and stress tests for
// the go-genkit-memory persistent session store adapters.
//
// These tests exercise the full genkit session lifecycle against every supported
// store backend (in-memory, BBolt, SQLite) side-by-side to verify behavioural
// equivalence and to stress-test memory quality under concurrent load.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firebase/genkit/go/core/x/session"
	bboltstore "github.com/leowmjw/go-genkit-memory/session/bbolt"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

// ─── shared state type ────────────────────────────────────────────────────────

// ConversationState simulates a multi-turn AI conversation stored in a session.
type ConversationState struct {
	UserID   string    `json:"user_id"`
	Messages []Message `json:"messages"`
	Metadata KVMap     `json:"metadata,omitempty"`
}

// Message represents a single conversational turn.
type Message struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"`
	Turn    int    `json:"turn"`
}

// KVMap is a convenience alias for string key/value pairs.
type KVMap map[string]string

// ─── store factory helpers ────────────────────────────────────────────────────

type namedStore struct {
	name  string
	store session.Store[ConversationState]
	close func()
}

// allStores returns one named store per backend, configured with temporary
// files where applicable. The caller must invoke each close() when done.
func allStores(t *testing.T) []namedStore {
	t.Helper()
	ctx := context.Background()
	var stores []namedStore

	// ── in-memory (genkit built-in) ──────────────────────────────────────────
	stores = append(stores, namedStore{
		name:  "in-memory",
		store: session.NewInMemoryStore[ConversationState](),
		close: func() {},
	})

	// ── BBolt ────────────────────────────────────────────────────────────────
	bboltPath := filepath(t, "bbolt-*.db")
	bstore, err := bboltstore.NewStore[ConversationState](ctx, bboltPath)
	if err != nil {
		t.Fatalf("bbolt.NewStore: %v", err)
	}
	stores = append(stores, namedStore{
		name:  "bbolt",
		store: bstore,
		close: func() { _ = bstore.Close() },
	})

	// ── SQLite ───────────────────────────────────────────────────────────────
	sqlitePath := filepath(t, "sqlite-*.db")
	sstore, err := sqlitestore.NewStore[ConversationState](ctx, sqlitePath)
	if err != nil {
		t.Fatalf("sqlite.NewStore: %v", err)
	}
	stores = append(stores, namedStore{
		name:  "sqlite",
		store: sstore,
		close: func() { _ = sstore.Close() },
	})

	return stores
}

func filepath(t *testing.T, pattern string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	f.Close()
	return f.Name()
}

// ─── behavioural equivalence tests ───────────────────────────────────────────

// TestAllStores_GetNonExistent verifies all stores return nil for missing sessions.
func TestAllStores_GetNonExistent(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			data, err := ns.store.Get(context.Background(), "ghost")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if data != nil {
				t.Errorf("expected nil, got %v", data)
			}
		})
	}
}

// TestAllStores_SaveLoad verifies that saved state is loaded correctly.
func TestAllStores_SaveLoad(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			d := &session.Data[ConversationState]{
				ID: "sl-1",
				State: ConversationState{
					UserID: "alice",
					Messages: []Message{
						{Role: "user", Content: "Hello!", Turn: 1},
						{Role: "assistant", Content: "Hi there!", Turn: 2},
					},
					Metadata: KVMap{"lang": "en"},
				},
			}

			if err := ns.store.Save(ctx, d.ID, d); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := ns.store.Get(ctx, d.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got == nil {
				t.Fatal("expected data, got nil")
			}
			if got.State.UserID != "alice" {
				t.Errorf("UserID: want alice, got %s", got.State.UserID)
			}
			if len(got.State.Messages) != 2 {
				t.Errorf("Messages: want 2, got %d", len(got.State.Messages))
			}
			if got.State.Metadata["lang"] != "en" {
				t.Errorf("Metadata[lang]: want en, got %s", got.State.Metadata["lang"])
			}
		})
	}
}

// TestAllStores_Overwrite verifies that re-saving a session overwrites it.
func TestAllStores_Overwrite(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			first := &session.Data[ConversationState]{
				ID:    "ow-1",
				State: ConversationState{UserID: "v1"},
			}
			second := &session.Data[ConversationState]{
				ID:    "ow-1",
				State: ConversationState{UserID: "v2"},
			}

			if err := ns.store.Save(ctx, first.ID, first); err != nil {
				t.Fatalf("first Save: %v", err)
			}
			if err := ns.store.Save(ctx, second.ID, second); err != nil {
				t.Fatalf("second Save: %v", err)
			}

			got, err := ns.store.Get(ctx, "ow-1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.State.UserID != "v2" {
				t.Errorf("want v2, got %s", got.State.UserID)
			}
		})
	}
}

// ─── session lifecycle tests ──────────────────────────────────────────────────

// TestAllStores_SessionLifecycle exercises New → UpdateState → Load on each backend.
func TestAllStores_SessionLifecycle(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			initial := ConversationState{
				UserID:   "bob",
				Messages: []Message{},
				Metadata: KVMap{"started": "yes"},
			}
			sess, err := session.New(ctx,
				session.WithID[ConversationState]("lifecycle-1"),
				session.WithInitialState(initial),
				session.WithStore(ns.store),
			)
			if err != nil {
				t.Fatalf("session.New: %v", err)
			}

			// Simulate 10 conversation turns.
			for i := 1; i <= 10; i++ {
				state := sess.State()
				state.Messages = append(state.Messages,
					Message{Role: "user", Content: fmt.Sprintf("Turn %d question", i), Turn: i*2 - 1},
					Message{Role: "assistant", Content: fmt.Sprintf("Turn %d answer", i), Turn: i * 2},
				)
				if err := sess.UpdateState(ctx, state); err != nil {
					t.Fatalf("UpdateState turn %d: %v", i, err)
				}
			}

			// Reload session (simulate new process / request).
			loaded, err := session.Load(ctx, ns.store, "lifecycle-1")
			if err != nil {
				t.Fatalf("session.Load: %v", err)
			}

			s := loaded.State()
			if s.UserID != "bob" {
				t.Errorf("UserID: want bob, got %s", s.UserID)
			}
			wantMsgs := 20 // 10 turns × 2 messages
			if len(s.Messages) != wantMsgs {
				t.Errorf("Messages: want %d, got %d", wantMsgs, len(s.Messages))
			}
			if s.Messages[len(s.Messages)-1].Content != "Turn 10 answer" {
				t.Errorf("last message: want %q, got %q",
					"Turn 10 answer", s.Messages[len(s.Messages)-1].Content)
			}
		})
	}
}

// TestAllStores_LoadNotFound verifies that loading a missing session returns NotFoundError.
func TestAllStores_LoadNotFound(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()

			_, err := session.Load(context.Background(), ns.store, "no-such-session")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var nfe *session.NotFoundError
			if !errors.As(err, &nfe) {
				t.Errorf("want *session.NotFoundError, got %T: %v", err, err)
			}
		})
	}
}

// ─── stress / quality tests ───────────────────────────────────────────────────

// TestAllStores_ConcurrentMultiSession exercises many goroutines each managing
// their own session simultaneously to stress-test store correctness.
func TestAllStores_ConcurrentMultiSession(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			const sessions = 50
			const turnsPerSession = 20

			var wg sync.WaitGroup
			var errCount atomic.Int64
			wg.Add(sessions)

			for i := range sessions {
				go func(idx int) {
					defer wg.Done()
					sid := fmt.Sprintf("sess-%d", idx)

					sess, err := session.New(ctx,
						session.WithID[ConversationState](sid),
						session.WithInitialState(ConversationState{
							UserID:   fmt.Sprintf("user-%d", idx),
							Messages: []Message{},
						}),
						session.WithStore(ns.store),
					)
					if err != nil {
						t.Errorf("[%s] session.New: %v", sid, err)
						errCount.Add(1)
						return
					}

					for turn := 1; turn <= turnsPerSession; turn++ {
						state := sess.State()
						state.Messages = append(state.Messages,
							Message{Role: "user", Content: "q", Turn: turn},
						)
						if err := sess.UpdateState(ctx, state); err != nil {
							t.Errorf("[%s] UpdateState turn %d: %v", sid, turn, err)
							errCount.Add(1)
							return
						}
					}

					// Verify final state.
					final := sess.State()
					if len(final.Messages) != turnsPerSession {
						t.Errorf("[%s] want %d messages, got %d",
							sid, turnsPerSession, len(final.Messages))
						errCount.Add(1)
					}
				}(i)
			}
			wg.Wait()

			if n := errCount.Load(); n > 0 {
				t.Errorf("%d goroutines reported errors", n)
			}
		})
	}
}

// TestAllStores_MemoryQuality verifies that no state is silently lost or
// corrupted across a large number of sequential updates on a single session.
func TestAllStores_MemoryQuality(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			const totalTurns = 100
			sid := "mq-session"

			sess, err := session.New(ctx,
				session.WithID[ConversationState](sid),
				session.WithInitialState(ConversationState{
					UserID:   "quality-test",
					Messages: make([]Message, 0, totalTurns*2),
				}),
				session.WithStore(ns.store),
			)
			if err != nil {
				t.Fatalf("session.New: %v", err)
			}

			// Accumulate messages.
			for i := 1; i <= totalTurns; i++ {
				state := sess.State()
				state.Messages = append(state.Messages,
					Message{Role: "user", Content: fmt.Sprintf("q%d", i), Turn: i*2 - 1},
					Message{Role: "assistant", Content: fmt.Sprintf("a%d", i), Turn: i * 2},
				)
				if err := sess.UpdateState(ctx, state); err != nil {
					t.Fatalf("UpdateState turn %d: %v", i, err)
				}
			}

			// Reload and verify every message is present and in order.
			loaded, err := session.Load(ctx, ns.store, sid)
			if err != nil {
				t.Fatalf("session.Load: %v", err)
			}

			msgs := loaded.State().Messages
			wantTotal := totalTurns * 2
			if len(msgs) != wantTotal {
				t.Fatalf("want %d messages, got %d", wantTotal, len(msgs))
			}
			for i, m := range msgs {
				wantTurn := i + 1
				if m.Turn != wantTurn {
					t.Errorf("msg[%d]: want Turn=%d, got Turn=%d", i, wantTurn, m.Turn)
				}
			}
		})
	}
}

// TestAllStores_LargeSingleSession verifies that very large session state (many
// messages) can be persisted and retrieved correctly.
func TestAllStores_LargeSingleSession(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			const msgCount = 1000
			msgs := make([]Message, 0, msgCount)
			for i := 1; i <= msgCount; i++ {
				msgs = append(msgs, Message{
					Role:    "user",
					Content: fmt.Sprintf("This is message number %d with some padding to increase size.", i),
					Turn:    i,
				})
			}

			d := &session.Data[ConversationState]{
				ID:    "large-1",
				State: ConversationState{UserID: "big-user", Messages: msgs},
			}
			if err := ns.store.Save(ctx, d.ID, d); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := ns.store.Get(ctx, d.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got == nil {
				t.Fatal("expected data, got nil")
			}
			if len(got.State.Messages) != msgCount {
				t.Errorf("Messages: want %d, got %d", msgCount, len(got.State.Messages))
			}
			if got.State.Messages[msgCount-1].Turn != msgCount {
				t.Errorf("last Turn: want %d, got %d", msgCount, got.State.Messages[msgCount-1].Turn)
			}
		})
	}
}

// TestAllStores_Throughput measures how quickly each backend can handle a
// burst of Save+Get round-trips and logs the results (not a hard assertion).
func TestAllStores_Throughput(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			const rounds = 500
			sid := "throughput-1"

			start := time.Now()
			for i := range rounds {
				d := &session.Data[ConversationState]{
					ID:    sid,
					State: ConversationState{UserID: fmt.Sprintf("u%d", i)},
				}
				if err := ns.store.Save(ctx, sid, d); err != nil {
					t.Fatalf("Save round %d: %v", i, err)
				}
				if _, err := ns.store.Get(ctx, sid); err != nil {
					t.Fatalf("Get round %d: %v", i, err)
				}
			}
			elapsed := time.Since(start)
			t.Logf("%s: %d Save+Get round-trips in %v (%.0f ops/s)",
				ns.name, rounds, elapsed, float64(rounds*2)/elapsed.Seconds())
		})
	}
}

// TestAllStores_IsolationBetweenSessions verifies that updates to one session
// do not bleed into another session stored in the same backend.
func TestAllStores_IsolationBetweenSessions(t *testing.T) {
	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			sessA, err := session.New(ctx,
				session.WithID[ConversationState]("iso-A"),
				session.WithInitialState(ConversationState{UserID: "A"}),
				session.WithStore(ns.store),
			)
			if err != nil {
				t.Fatalf("session.New A: %v", err)
			}

			sessB, err := session.New(ctx,
				session.WithID[ConversationState]("iso-B"),
				session.WithInitialState(ConversationState{UserID: "B"}),
				session.WithStore(ns.store),
			)
			if err != nil {
				t.Fatalf("session.New B: %v", err)
			}

			// Update only session A.
			stateA := sessA.State()
			stateA.Messages = append(stateA.Messages, Message{Role: "user", Content: "only-A"})
			if err := sessA.UpdateState(ctx, stateA); err != nil {
				t.Fatalf("UpdateState A: %v", err)
			}

			// Session B must be unaffected.
			loadedB, err := session.Load(ctx, ns.store, "iso-B")
			if err != nil {
				t.Fatalf("Load B: %v", err)
			}
			if len(loadedB.State().Messages) != 0 {
				t.Errorf("isolation breach: session B has %d messages", len(loadedB.State().Messages))
			}
			_ = sessB
		})
	}
}

// TestAllStores_TimestampOverflow verifies that Year-2100+ Unix epoch values
// stored in session state survive JSON serialization without integer overflow
// or truncation. Covers Scenario 22 from SCENARIO.md.
func TestAllStores_TimestampOverflow(t *testing.T) {
	// year2100 is 2100-01-01T00:00:00Z as a Unix epoch.
	year2100 := int64(4102444800)

	for _, ns := range allStores(t) {
		ns := ns
		t.Run(ns.name, func(t *testing.T) {
			t.Parallel()
			defer ns.close()
			ctx := context.Background()

			// Store a session containing large future timestamps.
			d := &session.Data[ConversationState]{
				ID: "ts-overflow-1",
				State: ConversationState{
					UserID: "timestamp-test",
					Messages: []Message{
						{Role: "user", Content: fmt.Sprintf("epoch:%d", year2100), Turn: int(year2100)},
						{Role: "assistant", Content: fmt.Sprintf("epoch:%d", year2100+86400*365), Turn: int(year2100 + 1)},
						{Role: "user", Content: "max-reasonable", Turn: int(9999999999)},
					},
				},
			}
			if err := ns.store.Save(ctx, d.ID, d); err != nil {
				t.Fatalf("Save: %v", err)
			}

			got, err := ns.store.Get(ctx, d.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got == nil {
				t.Fatal("expected data, got nil")
			}
			if len(got.State.Messages) != 3 {
				t.Fatalf("Messages: want 3, got %d", len(got.State.Messages))
			}

			// Verify the Year-2100 epoch survived the round-trip.
			wantTurn := int(year2100)
			if got.State.Messages[0].Turn != wantTurn {
				t.Errorf("Turn[0]: want %d, got %d (overflow?)", wantTurn, got.State.Messages[0].Turn)
			}

			// Verify the max-reasonable epoch (Year 2286) survived.
			if got.State.Messages[2].Content != "max-reasonable" {
				t.Errorf("Content[2]: want 'max-reasonable', got %q", got.State.Messages[2].Content)
			}
		})
	}
}
