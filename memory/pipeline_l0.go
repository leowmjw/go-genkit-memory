package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// writeJSONL is a function variable seam for testing.
// It appends records as newline-delimited JSON to the given path.
var writeJSONL = func(path string, records []L0MessageRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("pipeline_l0: mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("pipeline_l0: open: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for i := range records {
		if err := enc.Encode(&records[i]); err != nil {
			return fmt.Errorf("pipeline_l0: encode record: %w", err)
		}
	}
	return nil
}

// L0Recorder captures raw conversation turns and persists them to JSONL files.
// It is the entry point of the 4-tier pipeline — L0 is append-only and permissive.
type L0Recorder struct {
	dataDir string

	mu      sync.Mutex
	cursors map[string]time.Time // sessionKey → last captured timestamp
}

// NewL0Recorder creates a new L0 recorder writing to dataDir/conversations/.
func NewL0Recorder(dataDir string) *L0Recorder {
	return &L0Recorder{
		dataDir: dataDir,
		cursors: make(map[string]time.Time),
	}
}

// RecordConversation captures new messages for the given session.
// It uses a timestamp cursor to skip already-captured messages (incremental dedup).
// Returns the list of newly captured messages.
func (r *L0Recorder) RecordConversation(ctx context.Context, sessionKey string, messages []ConversationMessage) ([]L0MessageRecord, error) {
	_ = ctx // reserved for future use

	r.mu.Lock()
	cursor := r.cursors[sessionKey]
	r.mu.Unlock()

	// Position-slice: only process messages newer than our cursor.
	var newMsgs []ConversationMessage
	for _, m := range messages {
		if m.Timestamp.After(cursor) {
			newMsgs = append(newMsgs, m)
		}
	}

	if len(newMsgs) == 0 {
		return nil, nil
	}

	// Convert to L0 records.
	now := time.Now()
	records := make([]L0MessageRecord, len(newMsgs))
	for i, m := range newMsgs {
		records[i] = L0MessageRecord{
			ID:         generateID(),
			SessionKey: sessionKey,
			Role:       SanitizeRole(m.Role),
			Content:    m.Content,
			Timestamp:  m.Timestamp,
			CapturedAt: now,
		}
	}

	// Write to JSONL file.
	path := r.jsonlPath(now)
	if err := writeJSONL(path, records); err != nil {
		return nil, fmt.Errorf("pipeline_l0: write: %w", err)
	}

	// Update cursor to the latest timestamp.
	latest := newMsgs[len(newMsgs)-1].Timestamp
	r.mu.Lock()
	r.cursors[sessionKey] = latest
	r.mu.Unlock()

	return records, nil
}

// GetCursor returns the current timestamp cursor for the given session.
func (r *L0Recorder) GetCursor(sessionKey string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cursors[sessionKey]
}

// jsonlPath returns the JSONL file path for the given timestamp (one file per day).
func (r *L0Recorder) jsonlPath(t time.Time) string {
	filename := t.Format("2006-01-02") + ".jsonl"
	return filepath.Join(r.dataDir, "conversations", filename)
}

// generateID produces a random hex ID.
func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
