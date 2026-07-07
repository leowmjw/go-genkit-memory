package memory

import (
	"testing"
	"time"
)

func TestInMemoryStore_UpsertAndSearchL1Vector(t *testing.T) {
	store := NewInMemoryStore()

	rec := MemoryRecord{
		ID: "r1", Content: "user prefers Go", Type: MemoryTypePersona,
		Priority: 80, CreatedAt: time.Now(),
	}
	emb := []float32{1.0, 0.0, 0.0}

	if err := store.UpsertL1(rec, emb); err != nil {
		t.Fatalf("UpsertL1: %v", err)
	}

	// Search with same vector (perfect match).
	results, err := store.SearchL1Vector([]float32{1.0, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("SearchL1Vector: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Score < 0.99 {
		t.Errorf("score = %f, want ~1.0", results[0].Score)
	}
	if results[0].Record.ID != "r1" {
		t.Errorf("Record.ID = %q, want r1", results[0].Record.ID)
	}
}

func TestInMemoryStore_UpsertL1_UpdateExisting(t *testing.T) {
	store := NewInMemoryStore()

	rec := MemoryRecord{ID: "r1", Content: "v1", Type: MemoryTypePersona, CreatedAt: time.Now()}
	_ = store.UpsertL1(rec, []float32{1, 0})

	rec.Content = "v2"
	_ = store.UpsertL1(rec, []float32{0, 1})

	all, _ := store.GetAllL1()
	if len(all) != 1 {
		t.Fatalf("want 1 record after update, got %d", len(all))
	}
	if all[0].Content != "v2" {
		t.Errorf("Content = %q, want v2", all[0].Content)
	}
}

func TestInMemoryStore_SearchL1FTS(t *testing.T) {
	store := NewInMemoryStore()

	_ = store.UpsertL1(MemoryRecord{ID: "r1", Content: "user prefers Go programming", Type: MemoryTypePersona}, nil)
	_ = store.UpsertL1(MemoryRecord{ID: "r2", Content: "project uses PostgreSQL database", Type: MemoryTypeEpisodic}, nil)

	results, err := store.SearchL1FTS("Go", 10)
	if err != nil {
		t.Fatalf("SearchL1FTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 FTS result, got %d", len(results))
	}
	if results[0].Record.ID != "r1" {
		t.Errorf("Record.ID = %q, want r1", results[0].Record.ID)
	}
}

func TestInMemoryStore_DeleteL1Batch(t *testing.T) {
	store := NewInMemoryStore()

	_ = store.UpsertL1(MemoryRecord{ID: "r1", Content: "a"}, nil)
	_ = store.UpsertL1(MemoryRecord{ID: "r2", Content: "b"}, nil)
	_ = store.UpsertL1(MemoryRecord{ID: "r3", Content: "c"}, nil)

	if err := store.DeleteL1Batch([]string{"r1", "r3"}); err != nil {
		t.Fatalf("DeleteL1Batch: %v", err)
	}

	all, _ := store.GetAllL1()
	if len(all) != 1 {
		t.Fatalf("want 1 record after delete, got %d", len(all))
	}
	if all[0].ID != "r2" {
		t.Errorf("remaining ID = %q, want r2", all[0].ID)
	}
}

func TestInMemoryStore_VectorSearch_TopK(t *testing.T) {
	store := NewInMemoryStore()

	// Insert records with different embeddings.
	_ = store.UpsertL1(MemoryRecord{ID: "r1", Content: "a"}, []float32{1, 0, 0})
	_ = store.UpsertL1(MemoryRecord{ID: "r2", Content: "b"}, []float32{0, 1, 0})
	_ = store.UpsertL1(MemoryRecord{ID: "r3", Content: "c"}, []float32{0.9, 0.1, 0})

	// Query should return closest first.
	results, err := store.SearchL1Vector([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("SearchL1Vector: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	// r1 should be first (exact match), r3 second (close).
	if results[0].Record.ID != "r1" {
		t.Errorf("first result ID = %q, want r1", results[0].Record.ID)
	}
	if results[1].Record.ID != "r3" {
		t.Errorf("second result ID = %q, want r3", results[1].Record.ID)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{[]float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{[]float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{nil, nil, 0.0},
		{[]float32{1, 2}, []float32{1, 2, 3}, 0.0}, // different lengths
	}
	for _, tc := range cases {
		got := cosineSimilarity(tc.a, tc.b)
		if diff := got - tc.want; diff > 0.001 || diff < -0.001 {
			t.Errorf("cosineSimilarity(%v, %v) = %f, want %f", tc.a, tc.b, got, tc.want)
		}
	}
}
