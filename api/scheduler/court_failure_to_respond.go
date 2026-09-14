package scheduler

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/models"
)

// courtFailureToRespondSweep files citations and arrests that the civilian
// never answered, so a judge can actually see them.
//
// The problem it solves: a case reaches the judicial side only when the
// civilian CONTESTS it. Anyone who ignores a ticket never appears in front of a
// judge at all, so those cases are simply lost — communities have been
// reporting exactly this. The fine side already half-knows: inbox items flip to
// "delinquent" on a schedule, and nothing has ever consumed that status.
//
// Strictly opt-in per community. Filing cases changes what lands in a
// community's judicial queue, and a community that does not run courts should
// not wake up to a backlog.
//
// This job only makes cases VISIBLE. It suspends no licences and issues no
// warrants; a judge decides what happens next. Automatic consequences are a
// separate, separately-gated piece of work.
func (s *Scheduler) courtFailureToRespondSweep() {
	const jobName = "courtFailureToRespondSweep"
	s.recordStart(jobName)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	acquired, err := s.LockDB.TryAcquireLock(ctx, "court_failure_to_respond_sweep", s.instanceID, 6*time.Minute)
	if err != nil {
		zap.S().Errorw("failed to acquire lock for court FTR sweep", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "lock_acquire"})
		return
	}
	if !acquired {
		s.recordSkipped(jobName)
		return
	}
	defer s.LockDB.ReleaseLock(ctx, "court_failure_to_respond_sweep", s.instanceID)

	// Only communities that asked for this. Scoping the scan here is also what
	// keeps the cost sane: without it this would walk every civilian on the
	// platform every hour.
	cursor, err := s.CDB.Find(ctx, bson.M{"community.courtProcessing.autoFileUnanswered": true})
	if err != nil {
		zap.S().Errorw("court FTR sweep: failed to list opted-in communities", "error", err)
		s.recordError(jobName, err)
		SendCronAlert(s.instanceID, jobName, err, map[string]string{"phase": "list_communities"})
		return
	}
	defer cursor.Close(ctx)

	// DecodeCurrent in a Next loop, not Decode — Decode on this cursor wrapper
	// drains the whole thing with a background context.
	var communities []models.Community
	for cursor.Next(ctx) {
		var c models.Community
		if derr := cursor.DecodeCurrent(&c); derr != nil {
			zap.S().Warnw("court FTR sweep: skipping undecodable community", "error", derr)
			continue
		}
		communities = append(communities, c)
	}

	now := time.Now()
	totalFiled := 0
	for i := range communities {
		community := &communities[i]
		filed, cerr := s.fileUnansweredForCommunity(ctx, community, now)
		if cerr != nil {
			// One bad community must not stop the rest.
			zap.S().Warnw("court FTR sweep: community failed",
				"communityId", community.ID.Hex(), "error", cerr)
			continue
		}
		totalFiled += filed
	}

	if totalFiled > 0 {
		zap.S().Infow("court FTR sweep filed unanswered cases",
			"communities", len(communities), "casesFiled", totalFiled)
	}
	s.recordSuccess(jobName)
}

// fileUnansweredForCommunity files one court case per civilian, bundling every
// overdue item they have rather than one case per ticket — a judge wants the
// person, not a queue of fragments.
func (s *Scheduler) fileUnansweredForCommunity(ctx context.Context, community *models.Community, now time.Time) (int, error) {
	respondDays := models.ResolveRespondDays(community)
	communityID := community.ID.Hex()

	byCivilian := map[string][]models.ContestedItem{}
	names := map[string]string{}
	users := map[string]string{}

	// ── Arrest reports ────────────────────────────────────────────────────
	if s.ARDB != nil {
		reports, err := s.ARDB.Find(ctx, bson.M{"arrestReport.activeCommunityID": communityID})
		if err != nil {
			return 0, fmt.Errorf("list arrest reports: %w", err)
		}
		for i := range reports {
			r := &reports[i]
			cand := models.FailureToRespondCandidate{
				ItemID:      r.ID.Hex(),
				ItemType:    "arrest",
				Status:      r.Details.Status,
				CourtCaseID: r.Details.CourtCaseID,
				CreatedAt:   r.Details.CreatedAt.Time(),
			}
			if !models.EligibleForFailureToRespond(cand, now, respondDays) {
				continue
			}
			civID := r.Details.Arrestee.ID
			if civID == "" {
				continue
			}
			byCivilian[civID] = append(byCivilian[civID], models.ContestedItem{
				ItemID:   r.ID.Hex(),
				ItemType: "arrest",
			})
			if names[civID] == "" {
				names[civID] = r.Details.Arrestee.Name
			}
		}
	}

	// ── Criminal history (embedded on the civilian) ───────────────────────
	civilians, err := s.CivDB.Find(ctx, bson.M{"civilian.activeCommunityID": communityID})
	if err != nil {
		return 0, fmt.Errorf("list civilians: %w", err)
	}
	for i := range civilians {
		civ := &civilians[i]
		civID := civ.ID.Hex()
		for _, h := range civ.Details.CriminalHistory {
			cand := models.FailureToRespondCandidate{
				ItemID:      h.ID.Hex(),
				ItemType:    h.Type,
				Status:      h.Status,
				CourtCaseID: h.CourtCaseID,
				CreatedAt:   h.CreatedAt.Time(),
				Redacted:    h.Redacted,
			}
			if !models.EligibleForFailureToRespond(cand, now, respondDays) {
				continue
			}
			byCivilian[civID] = append(byCivilian[civID], models.ContestedItem{
				ItemID:   h.ID.Hex(),
				ItemType: "citation",
			})
			if names[civID] == "" {
				names[civID] = civilianName(civ)
			}
			if users[civID] == "" {
				users[civID] = civ.Details.UserID
			}
		}
	}

	filed := 0
	for civID, items := range byCivilian {
		if err := s.createFailureToRespondCase(ctx, communityID, civID, names[civID], users[civID], items, respondDays, now); err != nil {
			zap.S().Warnw("court FTR sweep: failed to file case",
				"communityId", communityID, "civilianId", civID, "error", err)
			continue
		}
		filed++
	}
	return filed, nil
}

// createFailureToRespondCase writes the case and stamps every item it covers so
// the next sweep skips them. The stamp is what makes this idempotent.
func (s *Scheduler) createFailureToRespondCase(
	ctx context.Context,
	communityID, civilianID, civilianName, userID string,
	items []models.ContestedItem,
	respondDays int,
	now time.Time,
) error {
	caseNumber, err := s.CourtDB.NextCaseNumber(ctx, communityID)
	if err != nil {
		return fmt.Errorf("next case number: %w", err)
	}

	nowDT := primitive.NewDateTimeFromTime(now)
	caseID := primitive.NewObjectID()
	statement := fmt.Sprintf(
		"Filed automatically: no response within %d day(s) of issue. The civilian did not contest these items.",
		respondDays,
	)

	courtCase := models.CourtCase{
		ID: caseID,
		Details: models.CourtCaseDetails{
			CaseNumber:     caseNumber,
			CivilianID:     civilianID,
			CivilianName:   civilianName,
			UserID:         userID,
			CommunityID:    communityID,
			ContestedItems: items,
			Statement:      statement,
			Origin:         models.CourtCaseOriginFailureToRespond,
			Status:         "submitted",
			History: []models.CourtCaseHistoryEntry{{
				Action:    "auto_filed_no_response",
				UserName:  "System",
				Timestamp: nowDT,
			}},
			CreatedAt: nowDT,
			UpdatedAt: nowDT,
		},
	}

	if _, err := s.CourtDB.InsertOne(ctx, courtCase); err != nil {
		return fmt.Errorf("insert court case: %w", err)
	}

	// Stamp the sources. A failure here means the item is re-filed next sweep,
	// which is why the case carries the same courtCaseID on every item: a
	// duplicate is recoverable, a lost case is not.
	civID, cerr := primitive.ObjectIDFromHex(civilianID)
	for _, it := range items {
		itemID, ierr := primitive.ObjectIDFromHex(it.ItemID)
		if ierr != nil {
			continue
		}
		if it.ItemType == "arrest" {
			_ = s.ARDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": bson.M{
				"arrestReport.courtCaseID": caseID.Hex(),
				"arrestReport.updatedAt":   nowDT,
			}})
			continue
		}
		if cerr != nil {
			continue
		}
		_ = s.CivDB.UpdateOne(ctx,
			bson.M{"_id": civID, "civilian.criminalHistory._id": itemID},
			bson.M{"$set": bson.M{
				"civilian.criminalHistory.$.courtCaseID": caseID.Hex(),
				"civilian.criminalHistory.$.updatedAt":   nowDT,
			}})
	}
	return nil
}

// civilianName mirrors the display-name fallback used elsewhere so an
// auto-filed case is labelled the same as a contested one.
func civilianName(c *models.Civilian) string {
	if c == nil {
		return ""
	}
	first, last := c.Details.FirstName, c.Details.LastName
	if first != "" && last != "" {
		return first + " " + last
	}
	if c.Details.Name != "" {
		return c.Details.Name
	}
	if first != "" {
		return first
	}
	return last
}
