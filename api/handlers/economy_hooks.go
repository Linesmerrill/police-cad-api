package handlers

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// inboxHookDeps is the minimum surface area needed to drop an inbox item
// for a civilian. Decoupled from Economy/Civilian/CourtCase handlers so
// citation and judicial hooks can reuse it.
type inboxHookDeps struct {
	IDB    databases.InboxItemDatabase
	CivDB  databases.CivilianDatabase
	CommDB databases.CommunityDatabase
}

// dropCitationInboxItem is called after a CriminalHistory entry is appended to a civilian.
// It runs in a fire-and-forget goroutine — never blocks the originating request and never
// causes the parent operation to fail.
//
//   - Skips if the entry isn't a citation, has no fines, or the community has economy disabled.
//   - In `auto_debit` fine mode, the item is created in `paid` state and the civilian's
//     balance is decremented in the same call (skipped when AllowNegativeBalance is false
//     and the civilian can't cover it — falls back to `pending`).
//   - Otherwise the item is created as `pending` with a due date derived from
//     `Economy.DefaultDueDays` (or 14 if unset).
func dropCitationInboxItem(deps inboxHookDeps, civilianID primitive.ObjectID, history models.CriminalHistory) {
	if deps.IDB == nil || deps.CivDB == nil || deps.CommDB == nil {
		return
	}
	if !isCitationType(history.Type) {
		return
	}
	totalDollars := 0
	for _, f := range history.Fines {
		if f.FineAmount > 0 {
			totalDollars += f.FineAmount
		}
	}
	if totalDollars <= 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		civ, err := deps.CivDB.FindOne(ctx, bson.M{"_id": civilianID})
		if err != nil || civ == nil {
			return
		}
		if civ.Details.ActiveCommunityID == "" {
			return
		}
		commID, err := primitive.ObjectIDFromHex(civ.Details.ActiveCommunityID)
		if err != nil {
			return
		}
		community, err := deps.CommDB.FindOne(ctx, bson.M{"_id": commID})
		if err != nil || community == nil {
			return
		}
		if !community.Details.Economy.Enabled {
			return
		}

		amountCents := int64(totalDollars) * 100
		now := primitive.NewDateTimeFromTime(time.Now())
		title := "Citation"
		if history.Type != "" {
			title = history.Type
		}
		fineNames := make([]string, 0, len(history.Fines))
		for _, f := range history.Fines {
			if f.FineType != "" {
				fineNames = append(fineNames, f.FineType)
			}
		}
		body := strings.Join(fineNames, ", ")

		item := models.InboxItem{
			ID:          primitive.NewObjectID(),
			CommunityID: civ.Details.ActiveCommunityID,
			UserID:      civ.Details.UserID,
			CivilianID:  civilianID.Hex(),
			Type:        "fine",
			Source:      "citation",
			Title:       title,
			Body:        body,
			Amount:      amountCents,
			Status:      "pending",
			IssuedBy:    history.OfficerID,
			RefType:     "criminalHistoryId",
			RefID:       history.ID.Hex(),
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		days := community.Details.Economy.DefaultDueDays
		if days <= 0 {
			days = 14
		}
		item.DueAt = primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, days))

		if community.Details.Economy.FineMode == "auto_debit" {
			// Lazy-init balance if needed so the comparison below uses the right baseline.
			if !civ.Details.BalanceInitialized {
				start := community.Details.Economy.DefaultStartingBalance
				_ = deps.CivDB.UpdateOne(ctx, bson.M{"_id": civilianID}, bson.M{
					"$set": bson.M{
						"civilian.balance":            start,
						"civilian.balanceInitialized": true,
						"civilian.updatedAt":          now,
					},
				})
				civ.Details.Balance = start
				civ.Details.BalanceInitialized = true
			}
			canCover := community.Details.Economy.AllowNegativeBalance || civ.Details.Balance >= amountCents
			if canCover {
				if err := deps.CivDB.UpdateOne(ctx, bson.M{"_id": civilianID}, bson.M{
					"$inc": bson.M{"civilian.balance": -amountCents},
					"$set": bson.M{
						"civilian.balanceInitialized": true,
						"civilian.updatedAt":          now,
					},
				}); err != nil {
					zap.S().Warnw("auto-debit failed; falling back to pending inbox item", "civilianId", civilianID.Hex(), "error", err)
				} else {
					item.Status = "paid"
					item.PaidAt = now
				}
			}
		}

		if _, err := deps.IDB.InsertOne(ctx, item); err != nil {
			zap.S().Errorw("failed to insert citation inbox item", "civilianId", civilianID.Hex(), "error", err)
		}
	}()
}

// dropJudicialInboxItem is called after a court case is resolved.
// For each "upheld" resolution that points at a CriminalHistory entry, it creates an inbox
// item summarizing the verdict and the fines that flow from it. Idempotent at the API level:
// callers should only invoke this from the final resolution path.
func dropJudicialInboxItem(deps inboxHookDeps, caseID, communityID, civilianID, userID, caseNumber string, resolutions []judicialResolution) {
	if deps.IDB == nil || deps.CivDB == nil || deps.CommDB == nil {
		return
	}
	if len(resolutions) == 0 || civilianID == "" || communityID == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		commOID, err := primitive.ObjectIDFromHex(communityID)
		if err != nil {
			return
		}
		community, err := deps.CommDB.FindOne(ctx, bson.M{"_id": commOID})
		if err != nil || community == nil || !community.Details.Economy.Enabled {
			return
		}

		civID, err := primitive.ObjectIDFromHex(civilianID)
		if err != nil {
			return
		}
		civ, err := deps.CivDB.FindOne(ctx, bson.M{"_id": civID})
		if err != nil || civ == nil {
			return
		}

		// Build a lookup from criminalHistory._id -> total fines (dollars)
		fineByHistoryID := map[string]int{}
		typeByHistoryID := map[string]string{}
		for _, h := range civ.Details.CriminalHistory {
			total := 0
			for _, f := range h.Fines {
				if f.FineAmount > 0 {
					total += f.FineAmount
				}
			}
			fineByHistoryID[h.ID.Hex()] = total
			typeByHistoryID[h.ID.Hex()] = h.Type
		}

		days := community.Details.Economy.DefaultDueDays
		if days <= 0 {
			days = 14
		}
		now := primitive.NewDateTimeFromTime(time.Now())
		dueAt := primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, days))

		for _, res := range resolutions {
			if !strings.EqualFold(res.Verdict, "upheld") {
				continue
			}
			dollars := fineByHistoryID[res.ItemID]
			if dollars <= 0 {
				continue
			}
			amountCents := int64(dollars) * 100

			title := "Court verdict"
			if t := typeByHistoryID[res.ItemID]; t != "" {
				title = t + " — verdict upheld"
			}
			body := res.JudgeNotes
			if body == "" && caseNumber != "" {
				body = "Case " + caseNumber
			}

			item := models.InboxItem{
				ID:          primitive.NewObjectID(),
				CommunityID: communityID,
				UserID:      userID,
				CivilianID:  civilianID,
				Type:        "verdict",
				Source:      "judicial",
				Title:       title,
				Body:        body,
				Amount:      amountCents,
				Status:      "pending",
				RefType:     "courtCaseId",
				RefID:       caseID,
				DueAt:       dueAt,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if _, err := deps.IDB.InsertOne(ctx, item); err != nil {
				zap.S().Errorw("failed to insert judicial inbox item", "caseId", caseID, "civilianId", civilianID, "error", err)
			}
		}
	}()
}

// judicialResolution mirrors the relevant fields of models.CaseResolution
// so this helper doesn't pull a cyclic dependency on the courtcase types.
type judicialResolution struct {
	ItemID     string
	ItemType   string
	Verdict    string
	JudgeNotes string
}

func isCitationType(t string) bool {
	if t == "" {
		return false
	}
	lt := strings.ToLower(t)
	return strings.Contains(lt, "citation") || strings.Contains(lt, "ticket")
}
