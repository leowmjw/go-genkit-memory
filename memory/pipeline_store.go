package memory

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// MemoryStore is the interface for persisting and querying L0/L1 memory records.
// Implementations must be safe for concurrent use.
type MemoryStore interface {
	// UpsertL0 stores an L0 message record with its embedding.
	UpsertL0(record L0MessageRecord, embedding []float32) error

	// UpsertL1 stores or updates an L1 memory record with its embedding.
	UpsertL1(record MemoryRecord, embedding []float32) error

	// SearchL1Vector searches L1 records by cosine similarity.
	SearchL1Vector(queryEmbedding []float32, topK int) ([]L1SearchResult, error)

	// SearchL1FTS searches L1 records by full-text keyword matching.
	SearchL1FTS(query string, topK int) ([]L1SearchResult, error)

	// DeleteL1Batch removes L1 records by their IDs.
	DeleteL1Batch(ids []string) error

	// GetAllL1 retrieves all L1 memory records (for L2 scene extraction).
	GetAllL1() ([]MemoryRecord, error)

	// Close releases any resources held by the store.
	Close() error
}

// ─── In-Memory Implementation ────────────────────────────────────────────────

// inMemoryStore is a simple in-memory implementation of MemoryStore for tests.
type inMemoryStore struct {
	mu        sync.RWMutex
	l0Records []l0Entry
	l1Records []l1Entry
}

type l0Entry struct {
	record    L0MessageRecord
	embedding []float32
}

type l1Entry struct {
	record    MemoryRecord
	embedding []float32
}

// NewInMemoryStore creates a new in-memory store suitable for testing.
func NewInMemoryStore() MemoryStore {
	return &inMemoryStore{}
}

func (s *inMemoryStore) UpsertL0(record L0MessageRecord, embedding []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.l0Records = append(s.l0Records, l0Entry{record: record, embedding: embedding})
	return nil
}

func (s *inMemoryStore) UpsertL1(record MemoryRecord, embedding []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update if exists.
	for i, e := range s.l1Records {
		if e.record.ID == record.ID {
			s.l1Records[i] = l1Entry{record: record, embedding: embedding}
			return nil
		}
	}
	// Insert new.
	s.l1Records = append(s.l1Records, l1Entry{record: record, embedding: embedding})
	return nil
}

func (s *inMemoryStore) SearchL1Vector(queryEmbedding []float32, topK int) ([]L1SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.l1Records) == 0 {
		return nil, nil
	}

	type scored struct {
		entry l1Entry
		score float64
	}

	var results []scored
	for _, e := range s.l1Records {
		if len(e.embedding) == 0 {
			continue
		}
		sim := cosineSimilarity(queryEmbedding, e.embedding)
		results = append(results, scored{entry: e, score: sim})
	}

	// Sort by score descending (simple insertion sort for small N).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if topK > len(results) {
		topK = len(results)
	}

	out := make([]L1SearchResult, topK)
	for i := range topK {
		out[i] = L1SearchResult{
			Record:    results[i].entry.record,
			Score:     results[i].score,
			MatchType: "vector",
		}
	}
	return out, nil
}

func (s *inMemoryStore) SearchL1FTS(query string, topK int) ([]L1SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryLower := strings.ToLower(query)
	words := strings.Fields(queryLower)

	var results []L1SearchResult
	for _, e := range s.l1Records {
		contentLower := strings.ToLower(e.record.Content)
		matched := false
		for _, w := range words {
			if strings.Contains(contentLower, w) {
				matched = true
				break
			}
		}
		if matched {
			results = append(results, L1SearchResult{
				Record:    e.record,
				Score:     1.0,
				MatchType: "fts",
			})
		}
	}

	if topK > len(results) {
		topK = len(results)
	}
	if topK > 0 {
		return results[:topK], nil
	}
	return nil, nil
}

func (s *inMemoryStore) DeleteL1Batch(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	filtered := s.l1Records[:0]
	for _, e := range s.l1Records {
		if !idSet[e.record.ID] {
			filtered = append(filtered, e)
		}
	}
	s.l1Records = filtered
	return nil
}

func (s *inMemoryStore) GetAllL1() ([]MemoryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := make([]MemoryRecord, len(s.l1Records))
	for i, e := range s.l1Records {
		records[i] = e.record
	}
	return records, nil
}

func (s *inMemoryStore) Close() error {
	return nil
}

// ─── Vector Math ─────────────────────────────────────────────────────────────

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// ─── BBolt-backed Implementation (stub) ──────────────────────────────────────

// BBoltMemoryStore wraps a bbolt database for persistent memory storage.
// TODO: Implement in a future phase using the existing go.etcd.io/bbolt dependency.
type BBoltMemoryStore struct {
	path string
}

// NewBBoltMemoryStore creates a new BBolt-backed memory store at the given path.
// TODO: Full implementation in future phase.
func NewBBoltMemoryStore(path string) (MemoryStore, error) {
	_ = path
	return nil, fmt.Errorf("pipeline_store: BBolt memory store not yet implemented")
}
