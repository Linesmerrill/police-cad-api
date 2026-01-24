package scheduler

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	templates "github.com/linesmerrill/police-cad-api/templates/html"
	"github.com/robfig/cron/v3"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// Scheduler handles periodic background jobs for the content creator program
type Scheduler struct {
	cron    *cron.Cron
	CCDB    databases.ContentCreatorDatabase
	SnapDB  databases.ContentCreatorSnapshotDatabase
	EntDB   databases.ContentCreatorEntitlementDatabase
	UDB     databases.UserDatabase
	CDB        databases.CommunityDatabase
	LockDB     databases.SchedulerLockDatabase
	instanceID string
}

// NewScheduler creates a new scheduler instance
func NewScheduler(
	ccDB databases.ContentCreatorDatabase,
	snapDB databases.ContentCreatorSnapshotDatabase,
	entDB databases.ContentCreatorEntitlementDatabase,
	uDB databases.UserDatabase,
	cDB databases.CommunityDatabase,
	lockDB databases.SchedulerLockDatabase,
) *Scheduler {
	// Generate a unique instance ID for this pod
	instanceID := os.Getenv("DYNO") // Heroku sets this to "web.1", "web.2", etc.
	if instanceID == "" {
		instanceID = fmt.Sprintf("instance-%d", time.Now().UnixNano())
	}

	return &Scheduler{
		cron:       cron.New(cron.WithLocation(time.UTC)),
		CCDB:       ccDB,
		SnapDB:     snapDB,
		EntDB:      entDB,
		UDB:        uDB,
		CDB:        cDB,
		LockDB:     lockDB,
		instanceID: instanceID,
	}
}

// Start begins the scheduler with all registered jobs
func (s *Scheduler) Start() {
	// Process grace period expirations and send reminders daily at 3 AM UTC
	_, err := s.cron.AddFunc("0 3 * * *", s.processGracePeriods)
	if err != nil {
		zap.S().Errorw("failed to register grace period job", "error", err)
	}

	// Check for creators who need grace period warnings every 3 days at 2 AM UTC
	// This catches creators whose follower counts may have dropped
	_, err = s.cron.AddFunc("0 2 */3 * *", s.checkAllCreators)
	if err != nil {
		zap.S().Errorw("failed to register creator check job", "error", err)
	}

	s.cron.Start()
	zap.S().Info("Content creator scheduler started")
}

// Stop gracefully stops the scheduler
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	zap.S().Info("Content creator scheduler stopped")
}

// processGracePeriods handles grace period expirations and sends reminder emails
func (s *Scheduler) processGracePeriods() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Try to acquire distributed lock (10 minute TTL)
	acquired, err := s.LockDB.TryAcquireLock(ctx, "grace_period_job", s.instanceID, 10*time.Minute)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for grace period job", "error", err)
		return
	}
	if !acquired {
		zap.S().Debug("Grace period job already running on another instance, skipping")
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "grace_period_job", s.instanceID)

	now := time.Now()
	oneDayFromNow := now.Add(24 * time.Hour)

	zap.S().Infow("Running grace period processing job", "instance", s.instanceID)

	// Find creators whose grace period has expired
	expiredFilter := bson.M{
		"status":           "warned",
		"gracePeriodEndsAt": bson.M{"$lt": primitive.NewDateTimeFromTime(now)},
	}

	cursor, err := s.CCDB.Find(ctx, expiredFilter)
	if err != nil {
		zap.S().Errorw("failed to find expired grace periods", "error", err)
		return
	}

	var expiredCreators []models.ContentCreator
	if err := cursor.All(ctx, &expiredCreators); err != nil {
		zap.S().Errorw("failed to decode expired creators", "error", err)
		return
	}
	cursor.Close(ctx)

	// Process each expired creator
	for _, creator := range expiredCreators {
		s.processExpiredCreator(ctx, creator)
	}

	// Find creators whose grace period ends in the next 24 hours (for reminder)
	reminderFilter := bson.M{
		"status": "warned",
		"gracePeriodEndsAt": bson.M{
			"$gt": primitive.NewDateTimeFromTime(now),
			"$lt": primitive.NewDateTimeFromTime(oneDayFromNow),
		},
		"gracePeriodNotifiedAt": nil, // Haven't sent reminder yet
	}

	cursor, err = s.CCDB.Find(ctx, reminderFilter)
	if err != nil {
		zap.S().Errorw("failed to find creators needing reminder", "error", err)
		return
	}

	var reminderCreators []models.ContentCreator
	if err := cursor.All(ctx, &reminderCreators); err != nil {
		zap.S().Errorw("failed to decode reminder creators", "error", err)
		return
	}
	cursor.Close(ctx)

	// Send reminder emails
	for _, creator := range reminderCreators {
		s.sendReminderEmail(ctx, creator)
	}

	zap.S().Infow("Grace period processing complete",
		"expiredProcessed", len(expiredCreators),
		"remindersSent", len(reminderCreators),
	)
}

// processExpiredCreator handles a creator whose grace period has expired
func (s *Scheduler) processExpiredCreator(ctx context.Context, creator models.ContentCreator) {
	// Check if they've recovered (maxFollowers >= 500)
	maxFollowers := 0
	for _, p := range creator.Platforms {
		if p.FollowerCount > maxFollowers {
			maxFollowers = p.FollowerCount
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())

	if maxFollowers >= 500 {
		// They recovered! Clear grace period and restore to active
		update := bson.M{
			"$set": bson.M{
				"status":                "active",
				"gracePeriodStartedAt":  nil,
				"gracePeriodEndsAt":     nil,
				"gracePeriodNotifiedAt": nil,
				"warningReason":         "",
				"warningMessage":        "",
				"warnedAt":              nil,
				"updatedAt":             now,
			},
		}
		err := s.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
		if err != nil {
			zap.S().Errorw("failed to restore creator", "error", err, "creatorId", creator.ID.Hex())
			return
		}

		// Send recovery email
		if creator.UserID != nil {
			go s.sendRecoveryEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers)
		}

		zap.S().Infow("Creator recovered during grace period check",
			"creatorId", creator.ID.Hex(),
			"maxFollowers", maxFollowers,
		)
		return
	}

	// They didn't recover - remove them
	s.removeCreator(ctx, creator, "Follower count remained below 500 for 30 days")
}

// removeCreator removes a creator from the program
func (s *Scheduler) removeCreator(ctx context.Context, creator models.ContentCreator, reason string) {
	now := primitive.NewDateTimeFromTime(time.Now())

	// Update creator status to removed
	update := bson.M{
		"$set": bson.M{
			"status":                "removed",
			"removalReason":         reason,
			"removedAt":             now,
			"gracePeriodStartedAt":  nil,
			"gracePeriodEndsAt":     nil,
			"gracePeriodNotifiedAt": nil,
			"updatedAt":             now,
		},
	}

	err := s.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
	if err != nil {
		zap.S().Errorw("failed to remove creator", "error", err, "creatorId", creator.ID.Hex())
		return
	}

	// Revoke all active entitlements
	entitlementUpdate := bson.M{
		"$set": bson.M{
			"active":       false,
			"revokedAt":    now,
			"revokeReason": "Creator removed from program: " + reason,
			"updatedAt":    now,
		},
	}
	err = s.EntDB.UpdateMany(ctx, bson.M{
		"contentCreatorId": creator.ID,
		"active":           true,
	}, entitlementUpdate)
	if err != nil {
		zap.S().Errorw("failed to revoke entitlements", "error", err, "creatorId", creator.ID.Hex())
	}

	// Update user's subscription if they have a personal entitlement
	if creator.UserID != nil {
		userUpdate := bson.M{
			"$set": bson.M{
				"user.subscription.plan":      "",
				"user.subscription.active":    false,
				"user.subscription.id":        "",
				"user.subscription.updatedAt": now,
			},
		}
		// Only update if the subscription was from content creator program
		_, err = s.UDB.UpdateOne(ctx, bson.M{
			"_id": *creator.UserID,
			"user.subscription.id": "cc_program_" + creator.ID.Hex(),
		}, userUpdate)
		if err != nil {
			zap.S().Warnw("failed to update user subscription", "error", err, "userId", creator.UserID.Hex())
		}
	}

	// Update community subscription if they have a community entitlement
	communityEnt, _ := s.EntDB.FindOne(ctx, bson.M{
		"contentCreatorId": creator.ID,
		"targetType":       "community",
	})
	if communityEnt != nil {
		communityUpdate := bson.M{
			"$set": bson.M{
				"community.subscription.plan":      "",
				"community.subscription.active":    false,
				"community.subscription.id":        "",
				"community.subscription.updatedAt": now,
			},
		}
		err = s.CDB.UpdateOne(ctx, bson.M{
			"_id": communityEnt.TargetID,
			"community.subscription.id": "cc_program_" + creator.ID.Hex(),
		}, communityUpdate)
		if err != nil {
			zap.S().Warnw("failed to update community subscription", "error", err, "communityId", communityEnt.TargetID.Hex())
		}
	}

	// Send removal email
	if creator.UserID != nil {
		go s.sendRemovalEmail(ctx, *creator.UserID, creator.DisplayName, reason)
	}

	zap.S().Infow("Creator removed from program",
		"creatorId", creator.ID.Hex(),
		"reason", reason,
	)
}

// checkAllCreators periodically checks all active creators for low follower counts
// This catches cases where followers may have dropped since their last sync
func (s *Scheduler) checkAllCreators() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Try to acquire distributed lock (15 minute TTL)
	acquired, err := s.LockDB.TryAcquireLock(ctx, "check_all_creators_job", s.instanceID, 15*time.Minute)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for creator check job", "error", err)
		return
	}
	if !acquired {
		zap.S().Debug("Creator check job already running on another instance, skipping")
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "check_all_creators_job", s.instanceID)

	zap.S().Infow("Running periodic creator follower check", "instance", s.instanceID)

	// Find active creators who haven't been synced in the last 3 days
	// and aren't already in a grace period
	threeDaysAgo := time.Now().Add(-3 * 24 * time.Hour)
	filter := bson.M{
		"status": "active",
		"gracePeriodStartedAt": nil,
		"$or": []bson.M{
			{"lastSyncedAt": nil},
			{"lastSyncedAt": bson.M{"$lt": primitive.NewDateTimeFromTime(threeDaysAgo)}},
		},
	}

	cursor, err := s.CCDB.Find(ctx, filter)
	if err != nil {
		zap.S().Errorw("failed to find creators for check", "error", err)
		return
	}

	var creators []models.ContentCreator
	if err := cursor.All(ctx, &creators); err != nil {
		zap.S().Errorw("failed to decode creators", "error", err)
		return
	}
	cursor.Close(ctx)

	lowFollowerCount := 0
	for _, creator := range creators {
		maxFollowers := 0
		for _, p := range creator.Platforms {
			if p.FollowerCount > maxFollowers {
				maxFollowers = p.FollowerCount
			}
		}

		// If below 500, start grace period
		if maxFollowers < 500 {
			s.startGracePeriod(ctx, creator, maxFollowers)
			lowFollowerCount++
		}
	}

	zap.S().Infow("Periodic creator check complete",
		"creatorsChecked", len(creators),
		"lowFollowersFound", lowFollowerCount,
	)
}

// startGracePeriod initiates the grace period for a creator with low followers
func (s *Scheduler) startGracePeriod(ctx context.Context, creator models.ContentCreator, maxFollowers int) {
	now := primitive.NewDateTimeFromTime(time.Now())
	gracePeriodEnd := primitive.NewDateTimeFromTime(time.Now().Add(30 * 24 * time.Hour))

	update := bson.M{
		"$set": bson.M{
			"status":               "warned",
			"gracePeriodStartedAt": now,
			"gracePeriodEndsAt":    gracePeriodEnd,
			"warningReason":        "low_followers",
			"warningMessage":       "Your highest follower count is below our minimum requirement of 500. You have 30 days to increase your followers or your creator account will be removed.",
			"warnedAt":             now,
			"updatedAt":            now,
		},
	}

	err := s.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, update)
	if err != nil {
		zap.S().Errorw("failed to start grace period", "error", err, "creatorId", creator.ID.Hex())
		return
	}

	// Send warning email
	if creator.UserID != nil {
		go s.sendLowFollowerWarningEmail(ctx, *creator.UserID, creator.DisplayName, maxFollowers, 500, 30)
	}

	zap.S().Infow("Started grace period for creator",
		"creatorId", creator.ID.Hex(),
		"maxFollowers", maxFollowers,
	)
}

// --- Email Helper Functions ---

func (s *Scheduler) sendEmail(toEmail, toName, subject, htmlContent, plainText string) error {
	from := mail.NewEmail("Lines Police CAD", "no-reply@linespolice-cad.com")
	to := mail.NewEmail(toName, toEmail)
	message := mail.NewSingleEmail(from, subject, to, plainText, htmlContent)
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		return err
	}
	if response.StatusCode >= 400 {
		zap.S().Errorw("sendgrid returned error status", "status", response.StatusCode, "body", response.Body)
	}
	return nil
}

func (s *Scheduler) getUserEmail(ctx context.Context, userID primitive.ObjectID) (email, name string) {
	var user struct {
		Details struct {
			Email    string `bson:"email"`
			Username string `bson:"username"`
		} `bson:"user"`
	}
	err := s.UDB.FindOne(ctx, bson.M{"_id": userID}).Decode(&user)
	if err != nil || user.Details.Email == "" {
		return "", ""
	}
	return user.Details.Email, user.Details.Username
}

func (s *Scheduler) sendLowFollowerWarningEmail(ctx context.Context, userID primitive.ObjectID, displayName string, currentFollowers, threshold, gracePeriodDays int) {
	email, _ := s.getUserEmail(ctx, userID)
	if email == "" {
		return
	}

	subject := "Action Required: Follower Count Below Minimum - Lines Police CAD"
	htmlContent := templates.RenderLowFollowerWarningEmail(displayName, currentFollowers, threshold, gracePeriodDays)
	plainText := "Your follower count has dropped below our minimum requirement. Please visit your dashboard to learn more."

	if err := s.sendEmail(email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send low follower warning email", "error", err, "userId", userID.Hex())
	}
}

func (s *Scheduler) sendReminderEmail(ctx context.Context, creator models.ContentCreator) {
	if creator.UserID == nil {
		return
	}

	email, _ := s.getUserEmail(ctx, *creator.UserID)
	if email == "" {
		return
	}

	maxFollowers := 0
	for _, p := range creator.Platforms {
		if p.FollowerCount > maxFollowers {
			maxFollowers = p.FollowerCount
		}
	}

	subject := "Final Reminder: Creator Account Removal Tomorrow - Lines Police CAD"
	htmlContent := templates.RenderGracePeriodReminderEmail(creator.DisplayName, maxFollowers, 500)
	plainText := "This is your final reminder. Your creator account will be removed tomorrow due to low follower count."

	if err := s.sendEmail(email, creator.DisplayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send reminder email", "error", err, "creatorId", creator.ID.Hex())
		return
	}

	// Mark as notified
	now := primitive.NewDateTimeFromTime(time.Now())
	s.CCDB.UpdateOne(ctx, bson.M{"_id": creator.ID}, bson.M{
		"$set": bson.M{"gracePeriodNotifiedAt": now},
	})

	zap.S().Infow("Sent grace period reminder email", "creatorId", creator.ID.Hex())
}

func (s *Scheduler) sendRecoveryEmail(ctx context.Context, userID primitive.ObjectID, displayName string, currentFollowers int) {
	email, _ := s.getUserEmail(ctx, userID)
	if email == "" {
		return
	}

	subject := "Great News: Your Account is Back in Good Standing! - Lines Police CAD"
	htmlContent := templates.RenderGracePeriodRecoveryEmail(displayName, currentFollowers)
	plainText := "Your follower count is now above the minimum requirement. Your creator account is back in good standing!"

	if err := s.sendEmail(email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send recovery email", "error", err, "userId", userID.Hex())
	}
}

func (s *Scheduler) sendRemovalEmail(ctx context.Context, userID primitive.ObjectID, displayName, reason string) {
	email, _ := s.getUserEmail(ctx, userID)
	if email == "" {
		return
	}

	subject := "Creator Program Removal Notice - Lines Police CAD"
	htmlContent := templates.RenderCreatorRemovedEmail(displayName, reason)
	plainText := "Your creator account has been removed due to: " + reason

	if err := s.sendEmail(email, displayName, subject, htmlContent, plainText); err != nil {
		zap.S().Errorw("failed to send removal email", "error", err, "userId", userID.Hex())
	}
}
