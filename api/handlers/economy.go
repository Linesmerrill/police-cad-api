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

// findCivilianMembership returns the rankId for a civilian in a civilian-kind department.
func findCivilianMembership(dept *models.Department, civilianID string) (rankID, userID string, found bool) {
	if dept == nil {
		return "", "", false
	}
	for _, m := range dept.CivilianMembers {
		if m.CivilianID == civilianID {
			return m.RankID, m.UserID, true
		}
	}
	return "", "", false
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

	sessUpdate := bson.M{
		"$inc": bson.M{
			"paidSeconds": durationSec,
			"earnings":    credit,
		},
		"$set": bson.M{
			"lastPayoutAt": endDT,
			"updatedAt":    nowDT,
		},
	}
	if terminalStatus != "" {
		sessUpdate["$set"].(bson.M)["status"] = terminalStatus
		sessUpdate["$set"].(bson.M)["endedAt"] = nowDT
	}
	if err := e.SDB.UpdateOne(ctx, bson.M{"_id": sess.ID}, sessUpdate); err != nil {
		return 0, endT, err
	}

	if credit > 0 && sess.CivilianID != "" {
		civID, err := primitive.ObjectIDFromHex(sess.CivilianID)
		if err == nil {
			_ = e.CivDB.UpdateOne(ctx, bson.M{"_id": civID}, bson.M{
				"$inc": bson.M{"civilian.balance": credit},
				"$set": bson.M{
					"civilian.balanceInitialized": true,
					"civilian.updatedAt":          nowDT,
				},
			})
		}
	}

	return credit, endT, nil
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

// ClockInHandler starts a clock session for a user (or civilian, for civilian-kind depts).
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

	var rankID string
	isCivilianDept := dept.DepartmentKind == "civilian"
	if isCivilianDept {
		if req.CivilianID == "" {
			config.ErrorStatus("civilianId required for civilian-kind department", http.StatusBadRequest, w, nil)
			return
		}
		var memberUserID string
		var found bool
		rankID, memberUserID, found = findCivilianMembership(dept, req.CivilianID)
		if !found || memberUserID != userID {
			config.ErrorStatus("civilian is not a member of this department", http.StatusForbidden, w, nil)
			return
		}
	} else {
		var found bool
		rankID, found = findUserMembership(dept, userID)
		// Public departments (approvalRequired=false) treat any community member
		// as implicitly eligible — no explicit join required. Private departments
		// still require an explicit member entry.
		if !found && !dept.ApprovalRequired {
			if _, inCommunity := community.Details.Members[userID]; inCommunity {
				found = true
			}
		}
		if !found {
			config.ErrorStatus("user is not a member of this department", http.StatusForbidden, w, nil)
			return
		}
	}

	// Re-resolve with rankID for accurate pay rate.
	if rankID != "" {
		_, payRate, _, _, _, _ = resolveDepartmentEconomy(community, req.DepartmentID, rankID)
	}

	// Enforce one active session per civilian (civilian dept) or user (user dept).
	activeFilter := bson.M{
		"status":       "active",
		"communityId":  req.CommunityID,
	}
	if isCivilianDept {
		activeFilter["civilianId"] = req.CivilianID
	} else {
		activeFilter["userId"] = userID
		activeFilter["civilianId"] = ""
	}
	if existing, _ := e.SDB.FindOne(ctx, activeFilter); existing != nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(existing)
		return
	}

	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	sess := models.ClockSession{
		ID:                primitive.NewObjectID(),
		CommunityID:       req.CommunityID,
		DepartmentID:      req.DepartmentID,
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
func (e Economy) GetActiveSessionHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	filter := bson.M{"status": "active"}
	if civID := r.URL.Query().Get("civilianId"); civID != "" {
		filter["civilianId"] = civID
	} else if uid := r.URL.Query().Get("userId"); uid != "" {
		filter["userId"] = uid
		filter["civilianId"] = ""
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
	if err := e.CivDB.UpdateOne(ctx, bson.M{"_id": cID}, bson.M{
		"$inc": bson.M{"civilian.balance": -item.Amount},
		"$set": bson.M{
			"civilian.balanceInitialized": true,
			"civilian.updatedAt":          now,
		},
	}); err != nil {
		config.ErrorStatus("failed to debit balance", http.StatusInternalServerError, w, err)
		return
	}

	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": bson.M{
		"status":    "paid",
		"paidAt":    now,
		"updatedAt": now,
	}}); err != nil {
		zap.S().Errorw("debit succeeded but failed to mark inbox item paid", "error", err, "itemId", id)
	}

	updated, _ := e.IDB.FindOne(ctx, bson.M{"_id": itemID})
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updated)
}

// DismissInboxItemHandler marks an inbox item dismissed. Admin-only is enforced at route level.
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
	now := primitive.NewDateTimeFromTime(time.Now())
	if err := e.IDB.UpdateOne(ctx, bson.M{"_id": itemID}, bson.M{"$set": bson.M{
		"status":      "dismissed",
		"dismissedAt": now,
		"dismissedBy": uid,
		"updatedAt":   now,
	}}); err != nil {
		config.ErrorStatus("failed to dismiss inbox item", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
