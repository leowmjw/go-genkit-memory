package memory

import (
	"sync"
	"time"
)

const defaultFallbackCapacity = 1000

// captureEntry is a buffered L0 capture that could not reach the gateway.
type captureEntry struct {
	SessionKey       string
	UserContent      string
	AssistantContent string
	CapturedAt       time.Time
}

// fallbackCache is a bounded ring buffer that holds capture events that failed
// to reach the gateway. It provides resilience during gateway outages and
// allows re-delivery once the gateway recovers.
//
// The ring buffer overwrites the oldest entry when full, ensuring bounded
// memory use under sustained gateway failure.
type fallbackCache struct {
	mu      sync.Mutex
	entries []captureEntry
	head    int // index of the next write position
	size    int // number of valid entries
	cap     int
}

// newFallbackCache creates a ring buffer with the given capacity.
func newFallbackCache(capacity int) *fallbackCache {
	if capacity <= 0 {
		capacity = defaultFallbackCapacity
	}
	return &fallbackCache{
		entries: make([]captureEntry, capacity),
		cap:     capacity,
	}
}

// Add inserts an entry into the ring buffer, evicting the oldest entry if full.
func (f *fallbackCache) Add(entry captureEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[f.head] = entry
	f.head = (f.head + 1) % f.cap
	if f.size < f.cap {
		f.size++
	}
}

// DrainAll returns all buffered entries and resets the buffer.
// Callers can use this to re-deliver cached events once the gateway recovers.
func (f *fallbackCache) DrainAll() []captureEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.size == 0 {
		return nil
	}
	out := make([]captureEntry, f.size)
	// The oldest entry is at (head - size + cap) % cap.
	start := (f.head - f.size + f.cap) % f.cap
	for i := range f.size {
		out[i] = f.entries[(start+i)%f.cap]
	}
	f.head = 0
	f.size = 0
	return out
}

// Len returns the number of buffered entries.
func (f *fallbackCache) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.size
}
