package scheduler

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// staleSessionSweep finds active clock sessions that have exceeded their max session window
// or missed their AFK heartbeat grace, finalizes payroll, and marks them expired.
func (s *Scheduler) staleSessionSweep() {
	const jobName = "staleSessionSweep"
	s.recordStart(jobName)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	acquired, err := s.LockDB.TryAcquireLock(ctx, "economy_stale_session_sweep", s.instanceID, 90*time.Second)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for stale session sweep", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "lock_acquire"})
		return
	}
	if !acquired {
		s.recordSkipped(jobName)
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "economy_stale_session_sweep", s.instanceID)

	sessions, err := s.SessionDB.Find(ctx, bson.M{"status": "active"})
	if err != nil {
		zap.S().Errorw("failed to find active sessions", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "find_active"})
		return
	}

	now := time.Now()
	swept := 0
	for i := range sessions {
		sess := &sessions[i]
		maxEnd := sess.StartedAt.Time().Add(time.Duration(sess.MaxSessionMinutes) * time.Minute)
		afkCutoff := sess.LastHeartbeatAt.Time().Add(time.Duration(sess.AfkGraceSeconds) * time.Second)
		if now.Before(maxEnd) && now.Before(afkCutoff) {
			continue
		}
		terminal := "expired"
		if now.Before(maxEnd) && !now.Before(afkCutoff) {
			terminal = "abandoned"
		}
		s.payAndCloseSession(ctx, sess, now, terminal)
		swept++
	}
	s.recordSuccess(jobName)
	if swept > 0 {
		zap.S().Infow("Economy: swept stale sessions", "count", swept)
	}
}

// payAndCloseSession is the scheduler's counterpart to the request-time paySession helper.
// Kept here to avoid a cyclic import on the handlers package.
func (s *Scheduler) payAndCloseSession(ctx context.Context, sess *models.ClockSession, now time.Time, terminalStatus string) {
	startCursor := sess.LastPayoutAt
	if startCursor == 0 {
		startCursor = sess.StartedAt
	}
	startT := startCursor.Time()
	maxEnd := sess.StartedAt.Time().Add(time.Duration(sess.MaxSessionMinutes) * time.Minute)
	endT := now
	if endT.After(maxEnd) {
		endT = maxEnd
	}
	if sess.LastHeartbeatAt != 0 {
		hbCap := sess.LastHeartbeatAt.Time().Add(time.Duration(sess.AfkGraceSeconds) * time.Second)
		if endT.After(hbCap) {
			endT = hbCap
		}
	}

	nowDT := primitive.NewDateTimeFromTime(now)
	var credit int64
	var durationSec int64

	if endT.After(startT) {
		durationSec = int64(endT.Sub(startT).Seconds())
		credit = sess.PayRateSnapshot * durationSec / 3600
		if credit < 0 {
			credit = 0
		}
	}

	update := bson.M{
		"$set": bson.M{
			"status":       terminalStatus,
			"endedAt":      nowDT,
			"lastPayoutAt": primitive.NewDateTimeFromTime(endT),
			"updatedAt":    nowDT,
		},
	}
	if durationSec > 0 || credit > 0 {
		update["$inc"] = bson.M{
			"paidSeconds": durationSec,
			"earnings":    credit,
		}
	}
	if err := s.SessionDB.UpdateOne(ctx, bson.M{"_id": sess.ID}, update); err != nil {
		zap.S().Errorw("failed to close session", "error", err, "sessionId", sess.ID.Hex())
		return
	}
	if credit > 0 && sess.CivilianID != "" {
		if civID, err := primitive.ObjectIDFromHex(sess.CivilianID); err == nil {
			_ = s.CivDB.UpdateOne(ctx, bson.M{"_id": civID}, bson.M{
				"$inc": bson.M{"civilian.balance": credit},
				"$set": bson.M{
					"civilian.balanceInitialized": true,
					"civilian.updatedAt":          nowDT,
				},
			})
		}
	}
}

// inboxDelinquencyTick flips any pending inbox item whose dueAt has passed to "delinquent".
func (s *Scheduler) inboxDelinquencyTick() {
	const jobName = "inboxDelinquencyTick"
	s.recordStart(jobName)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	acquired, err := s.LockDB.TryAcquireLock(ctx, "economy_inbox_delinquency_tick", s.instanceID, 90*time.Second)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for inbox delinquency tick", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "lock_acquire"})
		return
	}
	if !acquired {
		s.recordSkipped(jobName)
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "economy_inbox_delinquency_tick", s.instanceID)

	now := primitive.NewDateTimeFromTime(time.Now())
	filter := bson.M{
		"status": "pending",
		"dueAt":  bson.M{"$lt": now, "$gt": primitive.NewDateTimeFromTime(time.Unix(0, 0))},
	}
	update := bson.M{"$set": bson.M{"status": "delinquent", "updatedAt": now}}
	if err := s.InboxDB.UpdateMany(ctx, filter, update); err != nil {
		zap.S().Errorw("failed to flip inbox items to delinquent", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "update_many"})
		return
	}
	s.recordSuccess(jobName)
}
