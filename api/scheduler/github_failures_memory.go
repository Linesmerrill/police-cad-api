package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/linesmerrill/police-cad-api/databases"
)

// memoryFailureStreaks counts streaks in this process, for a scheduler built
// without a database: local runs and tests. A real deployment always has
// Mongo, which is the only place a streak survives the job moving dyno.
type memoryFailureStreaks struct {
	mu      sync.Mutex
	streaks map[string]databases.JobFailureStreak
}

func newMemoryFailureStreaks() *memoryFailureStreaks {
	return &memoryFailureStreaks{streaks: map[string]databases.JobFailureStreak{}}
}

func (m *memoryFailureStreaks) RecordFailure(_ context.Context, key string, now time.Time) (databases.JobFailureStreak, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	streak := m.streaks[key]
	streak.ID = key
	streak.Count++
	if streak.FirstFailedAt.IsZero() {
		streak.FirstFailedAt = now
	}
	streak.LastFailedAt = now
	m.streaks[key] = streak
	return streak, nil
}

func (m *memoryFailureStreaks) MarkAlerted(_ context.Context, key string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	streak := m.streaks[key]
	streak.AlertedAt = now
	m.streaks[key] = streak
	return nil
}

func (m *memoryFailureStreaks) ClearFailures(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streaks, key)
	return nil
}
