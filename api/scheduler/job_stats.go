package scheduler

import (
	"time"
)

// JobStat records the run history of a single cron job. All timestamps are
// UTC. A consumer (e.g. /health/scheduler) can detect staleness by
// comparing LastSucceededAt to the registered Schedule expectation.
type JobStat struct {
	Schedule        string    `json:"schedule"`
	LastStartedAt   time.Time `json:"lastStartedAt,omitempty"`
	LastSucceededAt time.Time `json:"lastSucceededAt,omitempty"`
	LastSkippedAt   time.Time `json:"lastSkippedAt,omitempty"`
	LastErroredAt   time.Time `json:"lastErroredAt,omitempty"`
	LastError       string    `json:"lastError,omitempty"`
	RunCount        int64     `json:"runCount"`
	SuccessCount    int64     `json:"successCount"`
	SkipCount       int64     `json:"skipCount"`
	ErrorCount      int64     `json:"errorCount"`
}

// registerJob adds an entry for jobName with its cron schedule expression
// so the health endpoint can surface "expected cadence" alongside the run
// history. Called from scheduler.Start() right after AddFunc.
func (s *Scheduler) registerJob(jobName, schedule string) {
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	if s.jobStats == nil {
		s.jobStats = map[string]*JobStat{}
	}
	if _, ok := s.jobStats[jobName]; !ok {
		s.jobStats[jobName] = &JobStat{}
	}
	s.jobStats[jobName].Schedule = schedule
}

// getOrInit lazily creates a JobStat so jobs that forgot to call
// registerJob still produce useful telemetry.
func (s *Scheduler) getOrInit(jobName string) *JobStat {
	if s.jobStats == nil {
		s.jobStats = map[string]*JobStat{}
	}
	stat, ok := s.jobStats[jobName]
	if !ok {
		stat = &JobStat{}
		s.jobStats[jobName] = stat
	}
	return stat
}

// recordStart marks a job as starting. Called as the first line of each job.
func (s *Scheduler) recordStart(jobName string) {
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	stat := s.getOrInit(jobName)
	stat.LastStartedAt = time.Now().UTC()
	stat.RunCount++
}

// recordSuccess marks a successful completion. Called at the end of the
// happy path.
func (s *Scheduler) recordSuccess(jobName string) {
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	stat := s.getOrInit(jobName)
	stat.LastSucceededAt = time.Now().UTC()
	stat.SuccessCount++
}

// recordSkipped marks a "lock held by another instance" early-return. Not
// an error — expected on multi-dyno deployments.
func (s *Scheduler) recordSkipped(jobName string) {
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	stat := s.getOrInit(jobName)
	stat.LastSkippedAt = time.Now().UTC()
	stat.SkipCount++
}

// recordError marks a failure with the error message. The Discord alert
// hook is separate — call SendCronAlert too if this is a meaningful
// failure (not a per-row best-effort one).
func (s *Scheduler) recordError(jobName string, err error) {
	if err == nil {
		return
	}
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	stat := s.getOrInit(jobName)
	stat.LastErroredAt = time.Now().UTC()
	stat.LastError = err.Error()
	stat.ErrorCount++
}

// SnapshotJobStats returns a copy of the current stats map for the health
// endpoint. Returning copies (not pointers) means consumers can iterate
// without holding the scheduler lock.
func (s *Scheduler) SnapshotJobStats() map[string]JobStat {
	s.jobStatsMu.Lock()
	defer s.jobStatsMu.Unlock()
	out := make(map[string]JobStat, len(s.jobStats))
	for name, stat := range s.jobStats {
		out[name] = *stat
	}
	return out
}

