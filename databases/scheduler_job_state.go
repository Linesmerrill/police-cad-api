package databases

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// schedulerJobStateCollectionName holds the small amount of state a scheduled
// job has to keep between ticks. It sits beside scheduler_locks and is keyed
// the same way, by a string the job chooses, so a streak that began on one
// dyno is still a streak after the job lands on another.
const schedulerJobStateCollectionName = "scheduler_job_state"

// JobFailureStreak is a run of consecutive failures of one job, counted across
// dynos.
//
// AlertedAt is the point in the streak where somebody was told. It is what
// makes an alert once-per-streak rather than once-per-tick: the streak is
// deleted on the next success, so the next run of failures alerts again.
type JobFailureStreak struct {
	ID            string    `bson:"_id"`
	Count         int       `bson:"count"`
	FirstFailedAt time.Time `bson:"firstFailedAt"`
	LastFailedAt  time.Time `bson:"lastFailedAt"`
	AlertedAt     time.Time `bson:"alertedAt,omitempty"`
}

// Alerted reports whether this streak has already been announced.
func (j JobFailureStreak) Alerted() bool { return !j.AlertedAt.IsZero() }

// SchedulerJobStateDatabase counts consecutive failures for a scheduled job.
type SchedulerJobStateDatabase interface {
	// RecordFailure counts one more failure and returns the streak as it now
	// stands.
	RecordFailure(ctx context.Context, key string, now time.Time) (JobFailureStreak, error)
	// MarkAlerted records that this streak has been announced.
	MarkAlerted(ctx context.Context, key string, now time.Time) error
	// ClearFailures ends the streak, which a success does.
	ClearFailures(ctx context.Context, key string) error
}

type schedulerJobStateDatabase struct {
	db DatabaseHelper
}

// NewSchedulerJobStateDatabase initializes the job state store.
func NewSchedulerJobStateDatabase(db DatabaseHelper) SchedulerJobStateDatabase {
	return &schedulerJobStateDatabase{db: db}
}

func (s *schedulerJobStateDatabase) RecordFailure(ctx context.Context, key string, now time.Time) (JobFailureStreak, error) {
	update := bson.M{
		"$inc":         bson.M{"count": 1},
		"$set":         bson.M{"lastFailedAt": now},
		"$setOnInsert": bson.M{"firstFailedAt": now},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var streak JobFailureStreak
	err := s.db.Collection(schedulerJobStateCollectionName).
		FindOneAndUpdate(ctx, bson.M{"_id": key}, update, opts).
		Decode(&streak)
	if err != nil {
		return JobFailureStreak{}, err
	}
	return streak, nil
}

func (s *schedulerJobStateDatabase) MarkAlerted(ctx context.Context, key string, now time.Time) error {
	_, err := s.db.Collection(schedulerJobStateCollectionName).
		UpdateOne(ctx, bson.M{"_id": key}, bson.M{"$set": bson.M{"alertedAt": now}})
	return err
}

func (s *schedulerJobStateDatabase) ClearFailures(ctx context.Context, key string) error {
	return s.db.Collection(schedulerJobStateCollectionName).DeleteOne(ctx, bson.M{"_id": key})
}
