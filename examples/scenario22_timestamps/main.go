// Scenario 22: Extended Multi-Day Timestamp Wrapping Boundaries
//
// Stores session state containing Year-2100 Unix epoch timestamps and verifies
// they round-trip through JSON serialization without integer overflow or truncation.
//
// Usage:
//
//	INTEGRATION_LIVE=1 go run ./examples/scenario22_timestamps
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/firebase/genkit/go/core/x/session"
	memstore "github.com/leowmjw/go-genkit-memory/memory"
	sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"
)

// year2100 is the Unix epoch for 2100-01-01T00:00:00Z.
var year2100 = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

type TimestampedState struct {
	Events []TimestampedEvent `json:"events"`
}

type TimestampedEvent struct {
	Name      string `json:"name"`
	UnixEpoch int64  `json:"unix_epoch"` // large future value
}

func main() {
	if os.Getenv("INTEGRATION_LIVE") != "1" {
		fmt.Println("SKIP: set INTEGRATION_LIVE=1 to run")
		os.Exit(0)
	}

	ctx := context.Background()
	store, err := sqlitestore.NewStore[TimestampedState](ctx, ":memory:")
	if err != nil {
		fail("open store: %v", err)
	}
	defer store.Close()

	adapter := memstore.NewAdapter[TimestampedState](store)
	defer adapter.Close()

	events := []TimestampedEvent{
		{Name: "project-deadline", UnixEpoch: year2100},
		{Name: "far-future-milestone", UnixEpoch: year2100 + 86400*365}, // +1 year
		{Name: "max-reasonable", UnixEpoch: 9999999999},                 // Year 2286
	}

	sess, err := session.New(ctx,
		session.WithID[TimestampedState]("timestamp-session-1"),
		session.WithInitialState(TimestampedState{Events: events}),
		session.WithStore[TimestampedState](adapter),
	)
	if err != nil {
		fail("create session: %v", err)
	}

	// Save and reload.
	state := sess.State()
	if err := sess.UpdateState(ctx, state); err != nil {
		fail("UpdateState: %v", err)
	}

	loaded, err := session.Load(ctx, adapter, "timestamp-session-1")
	if err != nil {
		fail("Load: %v", err)
	}

	gotEvents := loaded.State().Events
	if len(gotEvents) != len(events) {
		fail("event count: want %d, got %d", len(events), len(gotEvents))
	}

	var failures int
	for i, want := range events {
		got := gotEvents[i]
		if got.UnixEpoch != want.UnixEpoch {
			fmt.Fprintf(os.Stderr, "  epoch mismatch [%d] %q: want %d, got %d (delta=%d)\n",
				i, want.Name, want.UnixEpoch, got.UnixEpoch, got.UnixEpoch-want.UnixEpoch)
			failures++
		} else {
			t := time.Unix(got.UnixEpoch, 0).UTC()
			fmt.Printf("  OK [%d] %q = %s (epoch=%d)\n", i, got.Name, t.Format("2006-01-02"), got.UnixEpoch)
		}
	}

	// Also verify JSON encoding handles large integers without truncation.
	raw, _ := json.Marshal(TimestampedEvent{UnixEpoch: year2100})
	var decoded TimestampedEvent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		fail("JSON round-trip error: %v", err)
	}
	if decoded.UnixEpoch != year2100 {
		fail("JSON truncated year2100 epoch: want %d, got %d", year2100, decoded.UnixEpoch)
	}

	if failures > 0 {
		fail("%d timestamp overflow failures", failures)
	}
	fmt.Printf("PASS: %d Year-2100+ timestamps round-tripped correctly\n", len(events))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
