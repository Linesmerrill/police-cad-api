package scheduler

import (
	"context"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

const communityHardDeleteLockKey = "community_hard_delete_job"

// processCommunityPendingDeletions runs daily. Sends a 24-hour heads-up email to
// owners whose communities are about to be hard-deleted, then hard-deletes any
// community whose ScheduledDeletionAt has elapsed. Idempotent across instances
// via the shared scheduler lock.
func (s *Scheduler) processCommunityPendingDeletions() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	acquired, err := s.LockDB.TryAcquireLock(ctx, communityHardDeleteLockKey, s.instanceID, 25*time.Minute)
	if err != nil {
		zap.S().Errorw("community hard delete: failed to acquire lock", "error", err)
		s.SendCronAlert("processCommunityPendingDeletions", err, map[string]string{
			"phase": "lock_acquire",
		})
		return
	}
	if !acquired {
		zap.S().Debug("community hard delete job already running on another instance, skipping")
		return
	}
	defer s.LockDB.ReleaseLock(ctx, communityHardDeleteLockKey, s.instanceID)

	now := time.Now().UTC()
	nowDT := primitive.NewDateTimeFromTime(now)
	oneDayFromNowDT := primitive.NewDateTimeFromTime(now.Add(24 * time.Hour))

	zap.S().Infow("running community pending-deletion job", "instance", s.instanceID)

	s.sendCommunityDeletionReminders(ctx, nowDT, oneDayFromNowDT)
	s.hardDeleteExpiredPendingCommunities(ctx, nowDT)
}

// sendCommunityDeletionReminders emails owners whose community is within 24 hours
// of hard-deletion and hasn't already received the reminder.
func (s *Scheduler) sendCommunityDeletionReminders(ctx context.Context, nowDT, oneDayFromNowDT primitive.DateTime) {
	filter := bson.M{
		"community.pendingDeletionAt": bson.M{"$ne": nil},
		"community.scheduledDeletionAt": bson.M{
			"$gt": nowDT,
			"$lt": oneDayFromNowDT,
		},
		"community.pendingDeletionNotifiedAt": nil,
	}
	cursor, err := s.CDB.FindIncludingPending(ctx, filter)
	if err != nil {
		zap.S().Errorw("community hard delete: reminder query failed", "error", err)
		s.SendCronAlert("processCommunityPendingDeletions", err, map[string]string{
			"phase": "reminder_find",
		})
		return
	}
	defer cursor.Close(ctx)

	var due []models.Community
	if err := cursor.All(ctx, &due); err != nil {
		zap.S().Errorw("community hard delete: reminder decode failed", "error", err)
		s.SendCronAlert("processCommunityPendingDeletions", err, map[string]string{
			"phase": "reminder_decode",
		})
		return
	}

	for _, c := range due {
		s.sendPendingDeletionReminderEmail(ctx, c)
		if err := s.CDB.UpdateOne(ctx, bson.M{"_id": c.ID}, bson.M{
			"$set": bson.M{"community.pendingDeletionNotifiedAt": nowDT},
		}); err != nil {
			zap.S().Errorw("community hard delete: failed to mark reminder sent",
				"communityId", c.ID.Hex(), "error", err)
		}
	}
	zap.S().Infow("community pending-deletion reminders processed", "count", len(due))
}

// hardDeleteExpiredPendingCommunities performs the cascade hard-delete on any
// community whose ScheduledDeletionAt has elapsed.
func (s *Scheduler) hardDeleteExpiredPendingCommunities(ctx context.Context, nowDT primitive.DateTime) {
	filter := bson.M{
		"community.pendingDeletionAt":   bson.M{"$ne": nil},
		"community.scheduledDeletionAt": bson.M{"$lte": nowDT},
	}
	cursor, err := s.CDB.FindIncludingPending(ctx, filter)
	if err != nil {
		zap.S().Errorw("community hard delete: expired query failed", "error", err)
		s.SendCronAlert("processCommunityPendingDeletions", err, map[string]string{
			"phase": "expired_find",
		})
		return
	}
	defer cursor.Close(ctx)

	var expired []models.Community
	if err := cursor.All(ctx, &expired); err != nil {
		zap.S().Errorw("community hard delete: expired decode failed", "error", err)
		s.SendCronAlert("processCommunityPendingDeletions", err, map[string]string{
			"phase": "expired_decode",
		})
		return
	}

	for _, c := range expired {
		// Re-check the row with a guarded UpdateOne so a concurrent admin restore
		// (which $unset's scheduledDeletionAt) can't lose a race against the
		// hard-delete. The marker exists only on the doc we are about to wipe;
		// here we use it to atomically claim ownership of this row's deletion.
		err := s.CDB.UpdateOne(ctx, bson.M{
			"_id":                           c.ID,
			"community.scheduledDeletionAt": c.Details.ScheduledDeletionAt,
		}, bson.M{
			"$set": bson.M{"community.pendingDeletionNotifiedAt": nowDT},
		})
		if err != nil {
			zap.S().Warnw("community hard delete: could not claim row for deletion (likely restored)",
				"communityId", c.ID.Hex(), "error", err)
			continue
		}
		s.hardDeleteCommunityWithCascade(ctx, c.ID.Hex(), c.ID)
	}
	zap.S().Infow("community pending-deletion hard-deletes processed", "count", len(expired))
}

// sendPendingDeletionReminderEmail emails the community owner one day before
// their community is hard-deleted. Best-effort; logs and moves on.
func (s *Scheduler) sendPendingDeletionReminderEmail(ctx context.Context, c models.Community) {
	if c.Details.OwnerID == "" {
		return
	}
	ownerOID, err := primitive.ObjectIDFromHex(c.Details.OwnerID)
	if err != nil {
		// Some legacy owner IDs are stored as plain strings; in that case skip
		// the email rather than fail the sweep.
		return
	}
	email, displayName := s.getUserEmail(ctx, ownerOID)
	if email == "" {
		return
	}

	scheduled := time.Time{}
	if c.Details.ScheduledDeletionAt != nil {
		scheduled = c.Details.ScheduledDeletionAt.Time().UTC()
	}

	subject := "Final reminder: " + c.Details.Name + " deletes in 24 hours"
	htmlContent := templates.RenderCommunityPendingDeletionReminderEmail(displayName, c.Details.Name, scheduled)
	plainText := "Your community " + c.Details.Name + " is scheduled for permanent deletion in less than 24 hours. Contact support if you need it restored."

	if err := s.sendEmail(email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("community pending-deletion: reminder email failed",
			"communityId", c.ID.Hex(), "error", err)
	}
}

