# go-genkit-memory

Persistent session-store adapters for [Firebase Genkit Go](https://github.com/genkit-ai/genkit/tree/main/go), inspired by the pattern used by the [TencentDB Agent Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/main/hermes-plugin/memory/memory_tencentdb/README.md) Hermes plugin.

The built-in `session.InMemoryStore` provided by genkit-go is great for development and testing, but data is lost when the process restarts.  This module provides two drop-in replacements that persist session state to disk with **zero external infrastructure**:

| Backend | Package | Notes |
|---|---|---|
| [BBolt](https://github.com/etcd-io/bbolt) | `github.com/leowmjw/go-genkit-memory/session/bbolt` | Embedded key-value DB; pure Go |
| [SQLite](https://sqlite.org) | `github.com/leowmjw/go-genkit-memory/session/sqlite` | Embedded RDBMS; pure Go (no CGO) via [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) |

Both backends implement the `session.Store[S]` interface from
`github.com/firebase/genkit/go/core/x/session`, so you can swap them out with
a one-line change.

## Architecture

```
Your Genkit Application
    └─ session.New / session.Load
          └─ session.Store[S]  (interface)
               ├─ session.InMemoryStore  ← built-in, ephemeral
               ├─ bbolt.Store            ← this module, file-backed
               └─ sqlite.Store           ← this module, file-backed
```

This mirrors how the Hermes / TencentDB memory plugin attaches a durable
storage backend to an in-process memory manager: the application code calls the
same lifecycle methods (`New`, `Load`, `UpdateState`) regardless of which store
is in use.

## Installation

```
go get github.com/leowmjw/go-genkit-memory
```

## Quick Start

### BBolt

```go
import (
    "context"

    "github.com/firebase/genkit/go/core/x/session"
    bboltstore "github.com/leowmjw/go-genkit-memory/session/bbolt"
)

type ChatState struct {
    Messages []string `json:"messages"`
}

func main() {
    ctx := context.Background()

    store, err := bboltstore.NewStore[ChatState](ctx, "sessions.db")
    if err != nil {
        panic(err)
    }
    defer store.Close()

    // Create a new session (persisted immediately).
    sess, err := session.New(ctx,
        session.WithID[ChatState]("user-42"),
        session.WithInitialState(ChatState{}),
        session.WithStore(store),
    )
    if err != nil {
        panic(err)
    }

    // Append a message and persist.
    state := sess.State()
    state.Messages = append(state.Messages, "Hello!")
    sess.UpdateState(ctx, state)

    // Later (even in a different process) reload the session.
    loaded, err := session.Load(ctx, store, "user-42")
    if err != nil {
        panic(err)
    }
    fmt.Println(loaded.State().Messages) // ["Hello!"]
}
```

### SQLite

```go
import sqlitestore "github.com/leowmjw/go-genkit-memory/session/sqlite"

store, err := sqlitestore.NewStore[ChatState](ctx, "sessions.db")
// Usage is identical to the BBolt example above.
```

## Configuration

### BBolt options

| Option | Default | Description |
|---|---|---|
| `bbolt.WithBucket(name)` | `"genkit-sessions"` | BBolt bucket name |

### SQLite options

| Option | Default | Description |
|---|---|---|
| `sqlite.WithTable(name)` | `"genkit_sessions"` | SQLite table name |

Use `":memory:"` as the path to get an in-process, non-persistent SQLite
database (useful in tests).

## Testing

The module ships three levels of tests:

| Layer | Location | What it tests |
|---|---|---|
| Unit | `session/bbolt/` | BBolt store CRUD, persistence, concurrency |
| Unit | `session/sqlite/` | SQLite store CRUD, persistence, concurrency |
| Integration | `integration/` | All three backends side-by-side: behavioural equivalence, memory quality, throughput, isolation |

```
go test ./...
```

### Integration test highlights

* **`TestAllStores_MemoryQuality`** – Runs 100 conversation turns on a single session and verifies that every message is present and in order after a `session.Load`.
* **`TestAllStores_ConcurrentMultiSession`** – 50 goroutines each manage their own session simultaneously (20 turns each).
* **`TestAllStores_LargeSingleSession`** – Saves 1 000 messages in one session and reloads them.
* **`TestAllStores_Throughput`** – 500 Save+Get round-trips; logs ops/s per backend.
* **`TestAllStores_IsolationBetweenSessions`** – Ensures updates to session A are invisible to session B.

## Choosing a backend

| Criteria | In-memory | BBolt | SQLite |
|---|---|---|---|
| Persistence across restarts | ✗ | ✓ | ✓ |
| Multi-process access | ✗ | ✗ | ✓ (WAL mode) |
| Zero external deps | ✓ | ✓ | ✓ |
| SQL queries on state | ✗ | ✗ | ✓ |
| Recommended for | Tests, dev | Single-node prod | Single/multi-reader prod |

## License

Apache 2.0 — see [LICENSE](LICENSE).
