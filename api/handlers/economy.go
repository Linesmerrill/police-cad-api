package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Economy holds dependencies for the economy handlers (clock in/out, wallet, inbox).
type Economy struct {
	SDB    databases.ClockSessionDatabase
	IDB    databases.InboxItemDatabase
	CivDB  databases.CivilianDatabase
	CommDB databases.CommunityDatabase
	ACDB   databases.UserActiveCivilianDatabase // active-civilian pick; nil-safe — auto-pin on first clock-in is skipped when nil.
}

// ---- helpers ----

// resolveDepartmentEconomy returns the department + rank config snapshot used at clock-in.
// Falls back to department.BasePayPerHour when rank.PayRatePerHour is zero.
func resolveDepartmentEconomy(community *models.Community, deptID, rankID string) (dept *models.Department, payRate int64, payoutMode string, maxSessionMin, afkGrace int, ok bool) {
	if community == nil {
		return nil, 0, "", 0, 0, false
	}
	for i := range community.Details.Departments {
		d := &community.Details.Departments[i]
		if d.ID.Hex() == deptID {
			dept = d
			break
		}
	}
	if dept == nil {
		return nil, 0, "", 0, 0, false
	}
	payRate = dept.BasePayPerHour
	if rankID != "" {
		for i := range dept.Ranks {
			if dept.Ranks[i].ID.Hex() == rankID {
				if dept.Ranks[i].PayRatePerHour > 0 {
					payRate = dept.Ranks[i].PayRatePerHour
				}
				break
			}
		}
	}
	payoutMode = dept.PayoutMode
	if payoutMode == "" {
		payoutMode = "on_heartbeat"
	}
	maxSessionMin = dept.MaxSessionMinutes
	if maxSessionMin <= 0 {
		maxSessionMin = 120
	}
	afkGrace = dept.AfkGraceSeconds
	if afkGrace <= 0 {
		afkGrace = 60
	}
	return dept, payRate, payoutMode, maxSessionMin, afkGrace, true
}

// findUserMembership returns the rankId for a user in a department (LEO/EMS-style depts).
func findUserMembership(dept *models.Department, userID string) (rankID string, found bool) {
	if dept == nil {
		return "", false
	}
	for _, m := range dept.Members {
		if m.UserID == userID {
			return m.RankID, true
		}
	}
	return "", false
}


// paySession computes earnings to credit from session.LastPayoutAt (or StartedAt) up to `now`,
// capped at the session's MaxSession window. Updates the session document and the civilian balance.
// Safe to call repeatedly (idempotent on the time window).
func (e Economy) paySession(ctx context.Context, sess *models.ClockSession, now time.Time, terminalStatus string) (int64, time.Time, error) {
	startCursor := sess.LastPayoutAt
	if startCursor == 0 {
		startCursor = sess.StartedAt
	}
	startT := startCursor.Time()

	// Hard-cap to the session window from StartedAt.
	maxEnd := sess.StartedAt.Time().Add(time.Duration(sess.MaxSessionMinutes) * time.Minute)
	endT := now
	if endT.After(maxEnd) {
		endT = maxEnd
	}
	// Also cap to the last heartbeat + grace — no pay for time after AFK timeout.
	if sess.LastHeartbeatAt != 0 {
		hbCap := sess.LastHeartbeatAt.Time().Add(time.Duration(sess.AfkGraceSeconds) * time.Second)
		if endT.After(hbCap) {
			endT = hbCap
		}
	}

	if !endT.After(startT) {
		// Nothing to pay this tick.
		if terminalStatus != "" {
			endedAt := primitive.NewDateTimeFromTime(now)
			update := bson.M{"$set": bson.M{
				"status":    terminalStatus,
				"endedAt":   endedAt,
				"updatedAt": endedAt,
			}}
			if err := e.SDB.UpdateOne(ctx, bson.M{"_id": sess.ID}, update); err != nil {
				return 0, startT, err
			}
		}
		return 0, startT, nil
	}

	durationSec := int64(endT.Sub(startT).Seconds())
	// Earnings in cents: rate (cents/hr) * seconds / 3600
	credit := sess.PayRateSnapshot * durationSec / 3600
	if credit < 0 {
		credit = 0
	}

	nowDT := primitive.NewDateTimeFromTime(now)
	endDT := primitive.NewDateTimeFromTime(endT)

	// Apply the credit to the civilian first so we can capture the
	// post-credit balance and write it onto the session as balanceAfter.
	// This makes the unified transactions feed possible without recomputing
	// a running balance from the ledger every request.
	var balanceAfter int64
	balanceAfterSet := false
	if credit > 0 && sess.CivilianID != "" {
		if civID, perr := primitive.ObjectIDFromHex(sess.CivilianID); perr == nil {
			res := e.CivDB.FindOneAndUpdate(ctx,
				bson.M{"_id": civID},
				bson.M{
					"$inc": bson.M{"civilian.balance": credit},
					"$set": bson.M{
						"civilian.balanceInitialized": true,
						"civilian.updatedAt":          nowDT,
					},
				},
				options.FindOneAndUpdate().SetReturnDocument(options.After),
			)
			if res.Err() == nil {
				var updated models.Civilian
				if derr := res.Decode(&updated); derr == nil {
					balanceAfter = updated.Details.Balance
					balanceAfterSet = true
				}
			}
		}
	}

	sessSet := bson.M{
		"lastPayoutAt": endDT,
		"updatedAt":    nowDT,
	}
	if balanceAfterSet {
		sessSet["balanceAfter"] = balanceAfter
	}
	if terminalStatus != "" {
		sessSet["status"] = terminalStatus
		sessSet["endedAt"] = nowDT
	}
	sessUpdate := bson.M{
		"$inc": bson.M{
			"paidSeconds": durationSec,
			"earnings":    credit,
		},
		"$set": sessSet,
	}
	if err := e.SDB.UpdateOne(ctx, bson.M{"_id": sess.ID}, sessUpdate); err != nil {
		return 0, endT, err
	}

	return credit, endT, nil
}

// EndActiveSessionsForCivilian finalizes every active clock-in session attached
// to the given civilian: pays out accrued earnings up to now and marks the
// session ended. Returns the number of sessions that were ended.
//
// Call this before any operation that would orphan the session — most notably
// deleting the civilian. Without it, the active session keeps accruing
// time/earnings forever on the server while the client has no way to clock
// out (the civilian it points at is gone). Idempotent: if no sessions match,
// returns (0, nil) without error.
func (e Economy) EndActiveSessionsForCivilian(ctx context.Context, civilianID string) (int, error) {
	if civilianID == "" {
		return 0, nil
	}
	sessions, err := e.SDB.Find(ctx, bson.M{"civilianId": civilianID, "status": "active"})
	if err != nil {
		return 0, fmt.Errorf("list active sessions: %w", err)
	}
	if len(sessions) == 0 {
		return 0, nil
	}
	now := time.Now()
	ended := 0
	for i := range sessions {
		if _, _, perr := e.paySession(ctx, &sessions[i], now, "ended"); perr != nil {
			// Log and keep going — a single failure shouldn't block the
			// caller from finishing destructive work like a delete.
			zap.S().Warnw("failed to end active session before civilian cleanup",
				"sessionId", sessions[i].ID.Hex(),
				"civilianId", civilianID,
				"error", perr)
			continue
		}
		ended++
	}
	return ended, nil
}

// ensureBalanceInitialized lazy-backfills a civilian's balance from the community default
// on the first economy-aware read.
func (e Economy) ensureBalanceInitialized(ctx context.Context, civ *models.Civilian, community *models.Community) {
	if civ == nil || community == nil {
		return
	}
	if civ.Details.BalanceInitialized {
		return
	}
	start := community.Details.Economy.DefaultStartingBalance
	now := primitive.NewDateTimeFromTime(time.Now())
	_ = e.CivDB.UpdateOne(ctx, bson.M{"_id": civ.ID}, bson.M{
		"$set": bson.M{
			"civilian.balance":            start,
			"civilian.balanceInitialized": true,
			"civilian.updatedAt":          now,
		},
	})
	civ.Details.Balance = start
	civ.Details.BalanceInitialized = true
}

// ---- handlers ----

// clockInRequest is the body for POST /api/v2/economy/clock-in.
type clockInRequest struct {
	CommunityID  string `json:"communityId"`
	DepartmentID string `json:"departmentId"`
	CivilianID   string `json:"civilianId,omitempty"`
}

// ClockInHandler starts a clock session for a user against a department.
// Pay rate comes from rank.payRatePerHour if the user has a rank assigned,
// otherwise from department.basePayPerHour.
func (e Economy) ClockInHandler(w http.ResponseWriter, r *http.Request) {
	var req clockInRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.CommunityID == "" || req.DepartmentID == "" {
		config.ErrorStatus("communityId and departmentId required", http.StatusBadRequest, w, nil)
		return
	}
	userID := api.GetAuthenticatedUserIDFromContext(r.Context())
	if userID == "" {
		userID = r.URL.Query().Get("userId")
	}
	if userID == "" {
		config.ErrorStatus("missing authenticated user", http.StatusUnauthorized, w, nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	commID, err := primitive.ObjectIDFromHex(req.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}
	community, err := e.CommDB.FindOne(ctx, bson.M{"_id": commID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}
	if !community.Details.Economy.Enabled {
		config.ErrorStatus("economy is disabled for this community", http.StatusForbidden, w, nil)
		return
	}

	dept, payRate, payoutMode, maxSession, afkGrace, ok := resolveDepartmentEconomy(community, req.DepartmentID, "")
	if !ok {
		config.ErrorStatus("department not found", http.StatusNotFound, w, nil)
		return
	}
	if !dept.EconomyEnabled {
		config.ErrorStatus("economy is disabled for this department", http.StatusForbidden, w, nil)
		return
	}

	rankID, found := findUserMembership(dept, userID)
	// Public departments (approvalRequired=false) treat any community member
	// as implicitly eligible — no explicit join required.
	if !found && !dept.ApprovalRequired {
		if _, inCommunity := community.Details.Members[userID]; inCommunity {
			found = true
		}
	}
	if !found {
		config.ErrorStatus("user is not a member of this department", http.StatusForbidden, w, nil)
		return
	}
	// Re-resolve with rankID for accurate pay rate.
	if rankID != "" {
		_, payRate, _, _, _, _ = resolveDepartmentEconomy(community, req.DepartmentID, rankID)
	}

	// Enforce one active session per USER. The previous check scoped by
	// civilianId, which let a single user clock in N civilians at once and
	// accrue parallel earnings — clearly not the intent. A user can only
	// physically be at one job at a time, so any pre-existing active
	// session (any civilian, any community) blocks a new clock-in. The
	// client is expected to clock out the existing session first.
	if existing, _ := e.SDB.FindOne(ctx, bson.M{"status": "active", "userId": userID}); existing != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(existing)
		return
	}

	nowDT := primitive.NewDateTimeFromTime(time.Now())
	sess := models.ClockSession{
		ID:                primitive.NewObjectID(),
		CommunityID:       req.CommunityID,
		DepartmentID:      dept.ID.Hex(),
		DepartmentName:    dept.Name,
		UserID:            userID,
		CivilianID:        req.CivilianID,
		RankID:            rankID,
		PayRateSnapshot:   payRate,
		PayoutMode:        payoutMode,
		Status:            "active",
		StartedAt:         nowDT,
		LastHeartbeatAt:   nowDT,
		LastPayoutAt:      nowDT,
		MaxSessionMinutes: maxSession,
		AfkGraceSeconds:   afkGrace,
		CreatedAt:         nowDT,
		UpdatedAt:         nowDT,
	}
	if _, err := e.SDB.InsertOne(ctx, sess); err != nil {
		config.ErrorStatus("failed to create clock session", http.StatusInternalServerError, w, err)
		return
	}

	// Auto-pin: if the user hasn't picked an active civilian for this
	// community yet, pin the one they just clocked in. Makes "first time
	// you clock in is your default" the implicit behavior so users don't
	// have to discover the Set-as-active button separately. Idempotent —
	// when an active civ already exists we leave it alone (e.g. a user
	// explicitly picked Test Dude then temporarily clocks in Test Fam
	// shouldn't overwrite their pin). Best-effort — failures here don't
	// fail the clock-in itself.
	if e.ACDB != nil && req.CivilianID != "" {
		filter := bson.M{"userId": userID, "communityId": req.CommunityID}
		existing, _ := e.ACDB.FindOne(ctx, filter)
		if existing == nil || existing.CivilianID == "" {
			if uerr := e.ACDB.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
				"userId":      userID,
				"communityId": req.CommunityID,
				"civilianId":  req.CivilianID,
				"updatedAt":   nowDT,
			}}, options.Update().SetUpsert(true)); uerr != nil {
				zap.S().Warnw("failed to auto-pin active civilian on clock-in",
					"userId", userID,
					"communityId", req.CommunityID,
					"civilianId", req.CivilianID,
					"error", uerr)
			}
		}
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(sess)
}

// sessionIDRequest carries a session id in the body for clock-out / heartbeat.
type sessionIDRequest struct {
	SessionID string `json:"sessionId"`
}

func (e Economy) loadActiveSession(ctx context.Context, sessionID, userID string) (*models.ClockSession, error) {
	sID, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid sessionId: %w", err)
	}
	sess, err := e.SDB.FindOne(ctx, bson.M{"_id": sID})
	if err != nil {
		return nil, err
	}
	if sess.UserID != userID {
		return nil, fmt.Errorf("forbidden")
	}
	return sess, nil
}

// ClockOutHandler ends the session and finalizes payroll.
func (e Economy) ClockOutHandler(w http.ResponseWriter, r *http.Request) {
	var req sessionIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	userID := api.GetAuthenticatedUserIDFromContext(r.Context())
	if userID == "" {
		userID = r.URL.Query().Get("userId")
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	sess, err := e.loadActiveSession(ctx, req.SessionID, userID)
	if err != nil {
		config.ErrorStatus("session not found", http.StatusNotFound, w, err)
		return
	}
	if sess.Status != "active" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(sess)
		return
	}
	credited, _, err := e.paySession(ctx, sess, time.Now(), "ended")
	if err != nil {
		config.ErrorStatus("failed to finalize session", http.StatusInternalServerError, w, err)
		return
	}
	updated, _ := e.SDB.FindOne(ctx, bson.M{"_id": sess.ID})
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session":         updated,
		"creditedAmount":  credited,
	})
}

// HeartbeatHandler updates lastHeartbeatAt and (for on_heartbeat payoutMode) credits accrued pay.
func (e Economy) HeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	var req sessionIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	userID := api.GetAuthenticatedUserIDFromContext(r.Context())
	if userID == "" {
		userID = r.URL.Query().Get("userId")
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	sess, err := e.loadActiveSession(ctx, req.SessionID, userID)
	if err != nil {
		config.ErrorStatus("session not found", http.StatusNotFound, w, err)
		return
	}
	if sess.Status != "active" {
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(sess)
		return
	}

	now := time.Now()
	var credited int64
	if sess.PayoutMode == "on_heartbeat" {
		credited, _, err = e.paySession(ctx, sess, now, "")
		if err != nil {
			config.ErrorStatus("failed to pay session", http.StatusInternalServerError, w, err)
			return
		}
	}
	nowDT := primitive.NewDateTimeFromTime(now)
	_ = e.SDB.UpdateOne(ctx, bson.M{"_id": sess.ID}, bson.M{"$set": bson.M{
		"lastHeartbeatAt": nowDT,
		"updatedAt":       nowDT,
	}})

	// Auto-expire if past max session window.
	expiresAt := sess.StartedAt.Time().Add(time.Duration(sess.MaxSessionMinutes) * time.Minute)
	if now.After(expiresAt) {
		_, _, _ = e.paySession(ctx, sess, now, "expired")
	}

	updated, _ := e.SDB.FindOne(ctx, bson.M{"_id": sess.ID})
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session":        updated,
		"creditedAmount": credited,
	})
}

// GetActiveSessionHandler returns the active session for ?civilianId= or ?userId=.
//
// ?civilianId=X — scoped to a specific civilian (used by /wallet).
// ?userId=X     — returns the user's active session regardless of which
//                 civilian it's attached to. Pairs with the
//                 one-active-session-per-user rule in ClockInHandler so the
//                 dept dashboard can reflect a session started against any
//                 civilian. (Previously this branch also required
//                 civilianId=="" so it only matched user-only sessions; that
//                 made it impossible to detect a civilian-scoped session
//                 from the user-side caller.)
func (e Economy) GetActiveSessionHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	filter := bson.M{"status": "active"}
	if civID := r.URL.Query().Get("civilianId"); civID != "" {
		filter["civilianId"] = civID
	} else if uid := r.URL.Query().Get("userId"); uid != "" {
		filter["userId"] = uid
	} else {
		config.ErrorStatus("civilianId or userId required", http.StatusBadRequest, w, nil)
		return
	}
	sess, err := e.SDB.FindOne(ctx, filter)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("null"))
			return
		}
		config.ErrorStatus("failed to find session", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(sess)
}

// ListSessionsByCivilianHandler returns paginated shift history for a
// civilian. Newest-first, ended sessions only (active sessions are still
// running and surfaced through GetActiveSession).
//
//   GET /api/v2/economy/sessions/civilian/{civilianId}?page=1&limit=20
//
// Response: { data: []ClockSession, totalCount, page, limit }
func (e Economy) ListSessionsByCivilianHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilianId"]
	if civilianID == "" {
		config.ErrorStatus("civilianId required", http.StatusBadRequest, w, nil)
		return
	}
	page, limit := 1, 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{
		"civilianId": civilianID,
		"status":     bson.M{"$in": []string{"ended", "expired", "abandoned"}},
	}
	totalCount, _ := e.SDB.CountDocuments(ctx, filter)

	skip := int64((page - 1) * limit)
	lim := int64(limit)
	opts := options.Find().
		SetSort(bson.D{{Key: "startedAt", Value: -1}}).
		SetSkip(skip).
		SetLimit(lim)
	sessions, err := e.SDB.Find(ctx, filter, opts)
	if err != nil {
		config.ErrorStatus("failed to list sessions", http.StatusInternalServerError, w, err)
		return
	}
	if sessions == nil {
		sessions = []models.ClockSession{}
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       sessions,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
	})
}

// Transaction is a unified ledger entry surfaced by ListTransactionsHandler.
// Shifts and paid inbox items both contribute rows; future systems (shop
// purchases, judicial verdicts, admin grants) can extend the union by adding
// new ref types without changing the wire shape.
type Transaction struct {
	Type              string             `json:"type"`               // "shift" | "fine" | "fee" | "verdict" | "payroll" | "other"
	Direction         string             `json:"direction"`          // "credit" | "debit"
	Amount            int64              `json:"amount"`             // unsigned cents
	BalanceAfter      int64              `json:"balanceAfter"`       // 0 if unknown (legacy row)
	BalanceAfterKnown bool               `json:"balanceAfterKnown"`  // explicit so 0 isn't ambiguous
	Title             string             `json:"title"`
	Subtitle          string             `json:"subtitle,omitempty"`
	OccurredAt        primitive.DateTime `json:"occurredAt"`
	RefType           string             `json:"refType"` // "clockSession" | "inboxItem"
	RefID             string             `json:"refId"`
	// Shift-specific extras (omitted for non-shift rows).
	DepartmentName string `json:"departmentName,omitempty"`
	PaidSeconds    int64  `json:"paidSeconds,omitempty"`
	Status         string `json:"status,omitempty"`
}

// ListTransactionsHandler returns a unified, newest-first feed of every
// balance-affecting event for a civilian: ended clock sessions (credits) and
// paid inbox items (debits). Pagination merges both collections in memory —
// safe because each side is bounded to skip+limit before merge.
//
//	GET /api/v2/economy/transactions/civilian/{civilianId}?page=1&limit=20
//
// Response: { data: []Transaction, totalCount, page, limit }
func (e Economy) ListTransactionsHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilianId"]
	if civilianID == "" {
		config.ErrorStatus("civilianId required", http.StatusBadRequest, w, nil)
		return
	}
	page, limit := 1, 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	skip := (page - 1) * limit
	window := int64(skip + limit)

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	shiftFilter := bson.M{
		"civilianId": civilianID,
		"status":     bson.M{"$in": []string{"ended", "expired", "abandoned"}},
	}
	inboxFilter := bson.M{
		"civilianId": civilianID,
		"status":     "paid",
	}

	shiftTotal, _ := e.SDB.CountDocuments(ctx, shiftFilter)
	inboxTotal, _ := e.IDB.CountDocuments(ctx, inboxFilter)
	totalCount := shiftTotal + inboxTotal

	shifts, err := e.SDB.Find(ctx, shiftFilter,
		options.Find().SetSort(bson.D{{Key: "endedAt", Value: -1}}).SetLimit(window))
	if err != nil {
		config.ErrorStatus("failed to list shift transactions", http.StatusInternalServerError, w, err)
		return
	}
	inboxItems, err := e.IDB.Find(ctx, inboxFilter,
		options.Find().SetSort(bson.D{{Key: "paidAt", Value: -1}}).SetLimit(window))
	if err != nil {
		config.ErrorStatus("failed to list inbox transactions", http.StatusInternalServerError, w, err)
		return
	}

	merged := make([]Transaction, 0, len(shifts)+len(inboxItems))
	for i := range shifts {
		s := &shifts[i]
		occurredAt := s.EndedAt
		if occurredAt == 0 {
			occurredAt = s.UpdatedAt
		}
		merged = append(merged, Transaction{
			Type:              "shift",
			Direction:         "credit",
			Amount:            s.Earnings,
			BalanceAfter:      s.BalanceAfter,
			BalanceAfterKnown: s.BalanceAfter != 0,
			Title:             s.DepartmentName,
			Subtitle:          s.Status,
			OccurredAt:        occurredAt,
			RefType:           "clockSession",
			RefID:             s.ID.Hex(),
			DepartmentName:    s.DepartmentName,
			PaidSeconds:       s.PaidSeconds,
			Status:            s.Status,
		})
	}
	for i := range inboxItems {
		it := &inboxItems[i]
		occurredAt := it.PaidAt
		if occurredAt == 0 {
			occurredAt = it.UpdatedAt
		}
		txType := it.Type
		if txType == "" {
			txType = "other"
		}
		// InboxItem.Amount is signed: positive = owed (debit on pay),
		// negative = credit on pay (payroll, refund). Normalize to an
		// unsigned amount + an explicit direction so the UI can render
		// the +/- sign without re-checking the sign convention.
		direction := "debit"
		amt := it.Amount
		if amt < 0 {
			direction = "credit"
			amt = -amt
		}
		merged = append(merged, Transaction{
			Type:              txType,
			Direction:         direction,
			Amount:            amt,
			BalanceAfter:      it.BalanceAfter,
			BalanceAfterKnown: it.BalanceAfter != 0,
			Title:             it.Title,
			Subtitle:          it.Source,
			OccurredAt:        occurredAt,
			RefType:           "inboxItem",
			RefID:             it.ID.Hex(),
		})
	}

	// Newest-first by occurredAt; tie-break on RefID to make pagination stable.
	sortTransactionsDesc(merged)

	start := skip
	if start > len(merged) {
		start = len(merged)
	}
	end := start + limit
	if end > len(merged) {
		end = len(merged)
	}
	pageData := merged[start:end]
	if pageData == nil {
		pageData = []Transaction{}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       pageData,
		"totalCount": totalCount,
		"page":       page,
		"limit":      limit,
	})
}

func sortTransactionsDesc(txns []Transaction) {
	// Simple insertion sort — pages are tiny (≤ limit*2, capped at 200).
	for i := 1; i < len(txns); i++ {
		for j := i; j > 0; j-- {
			a, b := txns[j-1], txns[j]
			if a.OccurredAt > b.OccurredAt {
				break
			}
			if a.OccurredAt == b.OccurredAt && a.RefID > b.RefID {
				break
			}
			txns[j-1], txns[j] = b, a
		}
	}
}

// GetWalletHandler returns a civilian's wallet (balance + recent inbox items).
func (e Economy) GetWalletHandler(w http.ResponseWriter, r *http.Request) {
	civilianID := mux.Vars(r)["civilianId"]
	cID, err := primitive.ObjectIDFromHex(civilianID)
	if err != nil {
		config.ErrorStatus("invalid civilianId", http.StatusBadRequest, w, err)
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	civ, err := e.CivDB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("civilian not found", http.StatusNotFound, w, err)
		return
	}
	if civ.Details.ActiveCommunityID != "" {
		if commID, perr := primitive.ObjectIDFromHex(civ.Details.ActiveCommunityID); perr == nil {
			if community, cerr := e.CommDB.FindOne(ctx, bson.M{"_id": commID}); cerr == nil {
				e.ensureBalanceInitialized(ctx, civ, community)
			}
		}
	}

	recent, _ := e.IDB.Find(ctx, bson.M{"civilianId": civilianID}, options.Find().SetSort(bson.M{"createdAt": -1}).SetLimit(10))

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"civilianId":   civilianID,
		"balance":      civ.Details.Balance,
		"recentInbox":  recent,
	})
}

// ListInboxHandler returns paginated inbox items for ?userId= or ?civilianId=.
func (e Economy) ListInboxHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	filter := bson.M{}
	if uid := q.Get("userId"); uid != "" {
		filter["userId"] = uid
	}
	if cid := q.Get("civilianId"); cid != "" {
		filter["civilianId"] = cid
	}
	if status := q.Get("status"); status != "" {
		filter["status"] = status
	}
	if commID := q.Get("communityId"); commID != "" {
		filter["communityId"] = commID
	}
	if len(filter) == 0 {
		config.ErrorStatus("at least one filter (userId|civilianId|communityId) required", http.StatusBadRequest, w, nil)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	totalCount, _ := e.IDB.CountDocuments(ctx, filter)
	skip := int64((page - 1) * limit)
	items, err := e.IDB.Find(ctx, filter, options.Find().
		SetSort(bson.M{"createdAt": -1}).
		SetSkip(skip).
		SetLimit(int64(limit)),
	)
	if err != nil {
		config.ErrorStatus("failed to list inbox", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":       items,
		"page":       page,
		"limit":      limit,
		"totalCount": totalCount,
	})
}

// CreateInboxItemHandler creates a new inbox item. Used by citation/judicial/admin/shop flows.
func (e Economy) CreateInboxItemHandler(w http.ResponseWriter, r *http.Request) {
	var item models.InboxItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if item.CommunityID == "" || item.CivilianID == "" || item.UserID == "" {
		config.ErrorStatus("communityId, civilianId, userId required", http.StatusBadRequest, w, nil)
		return
	}
	if item.Type == "" {
		item.Type = "fee"
	}
	if item.Source == "" {
		item.Source = "admin"
	}
	if item.Status == "" {
		item.Status = "pending"
	}
	if item.IssuedBy == "" {
		if uid := api.GetAuthenticatedUserIDFromContext(r.Context()); uid != "" {
			item.IssuedBy = uid
		}
	}
	now := primitive.NewDateTimeFromTime(time.Now())
	item.ID = primitive.NewObjectID()
	item.CreatedAt = now
	item.UpdatedAt = now

	// Default due date from community settings.
	if item.DueAt == 0 {
		if commID, err := primitive.ObjectIDFromHex(item.CommunityID); err == nil {
			ctx, cancel := api.WithQueryTimeout(r.Context())
			defer cancel()
			if community, cerr := e.CommDB.FindOne(ctx, bson.M{"_id": commID}); cerr == nil {
				days := community.Details.Economy.DefaultDueDays
				if days <= 0 {
					days = 14
				}
				item.DueAt = primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, days))
			}
		}
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	if _, err := e.IDB.InsertOne(ctx, item); err != nil {
		config.ErrorStatus("failed to create inbox item", http.StatusInternalServerError, w, err)
		return
	}
	go BroadcastInboxEvent("inbox.created", item.CommunityID, item)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(item)
}

// PayInboxItemHandler debits the civilian's balance and marks the item paid.
func (e Economy) PayInboxItemHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	itemID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid item id", http.StatusBadRequest, w, err)
		return
	}
	userID := api.GetAuthenticatedUserIDFromContext(r.Context())
	if userID == "" {
		userID = r.URL.Query().Get("userId")
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	item, err := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if err != nil {
		config.ErrorStatus("inbox item not found", http.StatusNotFound, w, err)
		return
	}
	if item.Status == "paid" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(item)
		return
	}
	if item.UserID != userID {
		config.ErrorStatus("forbidden", http.StatusForbidden, w, nil)
		return
	}

	cID, err := primitive.ObjectIDFromHex(item.CivilianID)
	if err != nil {
		config.ErrorStatus("invalid civilianId on item", http.StatusBadRequest, w, err)
		return
	}
	civ, err := e.CivDB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("civilian not found", http.StatusNotFound, w, err)
		return
	}

	// Check community policy on negative balances.
	allowNegative := false
	if commID, perr := primitive.ObjectIDFromHex(item.CommunityID); perr == nil {
		if community, cerr := e.CommDB.FindOne(ctx, bson.M{"_id": commID}); cerr == nil {
			allowNegative = community.Details.Economy.AllowNegativeBalance
			e.ensureBalanceInitialized(ctx, civ, community)
		}
	}
	if !allowNegative && civ.Details.Balance < item.Amount {
		config.ErrorStatus("insufficient balance", http.StatusPaymentRequired, w, nil)
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	// Atomic debit so we can capture the post-debit balance and stamp it onto
	// the inbox item as balanceAfter for the transactions feed.
	res := e.CivDB.FindOneAndUpdate(ctx,
		bson.M{"_id": cID},
		bson.M{
			"$inc": bson.M{"civilian.balance": -item.Amount},
			"$set": bson.M{
				"civilian.balanceInitialized": true,
				"civilian.updatedAt":          now,
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	if res.Err() != nil {
		config.ErrorStatus("failed to debit balance", http.StatusInternalServerError, w, res.Err())
		return
	}
	var updatedCiv models.Civilian
	var balanceAfter int64
	balanceAfterSet := false
	if derr := res.Decode(&updatedCiv); derr == nil {
		balanceAfter = updatedCiv.Details.Balance
		balanceAfterSet = true
	}

	itemSet := bson.M{
		"status":    "paid",
		"paidAt":    now,
		"updatedAt": now,
	}
	if balanceAfterSet {
		itemSet["balanceAfter"] = balanceAfter
	}
	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": itemSet}); err != nil {
		zap.S().Errorw("debit succeeded but failed to mark inbox item paid", "error", err, "itemId", id)
	}

	updated, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if updated != nil {
		go BroadcastInboxEvent("inbox.updated", updated.CommunityID, updated)
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updated)
}

// DismissInboxItemHandler marks an inbox item dismissed. Admin-only is enforced at route level.
// When used to resolve a contested fine, sets resolution="dismissed" so the
// civilian's inbox UI can render the "Dismissed" state distinctly.
func (e Economy) DismissInboxItemHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	itemID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid item id", http.StatusBadRequest, w, err)
		return
	}
	uid := api.GetAuthenticatedUserIDFromContext(r.Context())
	if uid == "" {
		uid = r.URL.Query().Get("userId")
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existing, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	now := primitive.NewDateTimeFromTime(time.Now())
	updates := bson.M{
		"status":      "dismissed",
		"dismissedAt": now,
		"dismissedBy": uid,
		"updatedAt":   now,
	}
	if existing != nil && existing.Status == "contested" {
		updates["resolvedAt"] = now
		updates["resolvedBy"] = uid
		updates["resolution"] = "dismissed"
	}
	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": updates}); err != nil {
		config.ErrorStatus("failed to dismiss inbox item", http.StatusInternalServerError, w, err)
		return
	}
	if updated, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID}); updated != nil {
		go BroadcastInboxEvent("inbox.updated", updated.CommunityID, updated)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ContestInboxItemHandler lets a civilian (or the authenticated user owning
// the inbox item) contest a pending fine. Status moves to "contested", the
// original due date is preserved, and the active due date is extended by
// community.economy.contestExtensionDays (default 30).
func (e Economy) ContestInboxItemHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	itemID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid item id", http.StatusBadRequest, w, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	item, err := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if err != nil || item == nil {
		config.ErrorStatus("inbox item not found", http.StatusNotFound, w, err)
		return
	}
	if item.Status != "pending" && item.Status != "delinquent" {
		config.ErrorStatus("only pending fines can be contested", http.StatusBadRequest, w, fmt.Errorf("status=%s", item.Status))
		return
	}
	// Once a verdict has been entered (resolution set) the case is closed;
	// re-contesting would loop the civilian back through the judicial queue.
	if item.Resolution != "" {
		config.ErrorStatus("this fine has already been ruled on and cannot be contested again", http.StatusBadRequest, w, fmt.Errorf("resolution=%s", item.Resolution))
		return
	}

	// Resolve extension days from community settings.
	days := 30
	if commID, perr := primitive.ObjectIDFromHex(item.CommunityID); perr == nil {
		if community, cerr := e.CommDB.FindOne(ctx, bson.M{"_id": commID}); cerr == nil {
			if d := community.Details.Economy.ContestExtensionDays; d > 0 {
				days = d
			}
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	newDue := primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, days))
	updates := bson.M{
		"status":        "contested",
		"contestedAt":   now,
		"contestReason": body.Reason,
		"dueAt":         newDue,
		"updatedAt":     now,
	}
	// Preserve the original due date so we can show it alongside the extension.
	if item.OriginalDueAt == 0 && item.DueAt != 0 {
		updates["originalDueAt"] = item.DueAt
	}
	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": updates}); err != nil {
		config.ErrorStatus("failed to contest inbox item", http.StatusInternalServerError, w, err)
		return
	}
	updated, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if updated != nil {
		go BroadcastInboxEvent("inbox.updated", updated.CommunityID, updated)
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updated)
}

// UpholdInboxItemHandler resolves a contested fine in favor of the issuer:
// the fine reverts to "pending" with the extended due date preserved (so the
// civilian still has time to pay after losing the contest).
func (e Economy) UpholdInboxItemHandler(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	itemID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		config.ErrorStatus("invalid item id", http.StatusBadRequest, w, err)
		return
	}
	uid := api.GetAuthenticatedUserIDFromContext(r.Context())
	if uid == "" {
		uid = r.URL.Query().Get("userId")
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	item, err := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if err != nil || item == nil {
		config.ErrorStatus("inbox item not found", http.StatusNotFound, w, err)
		return
	}
	if item.Status != "contested" {
		config.ErrorStatus("only contested fines can be upheld", http.StatusBadRequest, w, fmt.Errorf("status=%s", item.Status))
		return
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": bson.M{
		"status":     "pending",
		"resolvedAt": now,
		"resolvedBy": uid,
		"resolution": "upheld",
		"updatedAt":  now,
	}}); err != nil {
		config.ErrorStatus("failed to uphold inbox item", http.StatusInternalServerError, w, err)
		return
	}
	updated, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	if updated != nil {
		go BroadcastInboxEvent("inbox.updated", updated.CommunityID, updated)
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updated)
}

// GetInboxPendingCountsHandler returns the count of "needs attention"
// (pending + delinquent + contested) inbox items per civilian for a given
// owning user, scoped to a community. Used by the dept-dashboard civilian
// grid to badge each card with the number of unhandled items.
//   GET /api/v2/economy/inbox/pending-counts?userId=X&communityId=Y
// Response: { "counts": { "<civilianId>": <number>, ... } }
func (e Economy) GetInboxPendingCountsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	userID := q.Get("userId")
	commID := q.Get("communityId")
	if userID == "" || commID == "" {
		config.ErrorStatus("userId and communityId required", http.StatusBadRequest, w, nil)
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	items, err := e.IDB.Find(ctx, bson.M{
		"userId":      userID,
		"communityId": commID,
		"status":      bson.M{"$in": []string{"pending", "delinquent", "contested"}},
	})
	if err != nil {
		config.ErrorStatus("failed to count inbox items", http.StatusInternalServerError, w, err)
		return
	}
	counts := map[string]int{}
	for _, it := range items {
		if it.CivilianID == "" {
			continue
		}
		counts[it.CivilianID]++
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"counts": counts})
}
