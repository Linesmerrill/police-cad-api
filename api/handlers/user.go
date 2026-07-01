package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v82"
	portalsession "github.com/stripe/stripe-go/v82/billingportal/session"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// User exported for testing purposes
type User struct {
	DB    databases.UserDatabase
	CDB   databases.CommunityDatabase
	EntDB databases.ContentCreatorEntitlementDatabase
	PTDB  databases.PushTokenDatabase
	ALDB  databases.AuditLogDatabase
	UPDB  databases.UserPreferencesDatabase
	SEDB  databases.SubscriptionEventDatabase
	ACDB  databases.UserActiveCivilianDatabase // active-civilian-per-community pick, shared with the Discord bot
	SDB   databases.ClockSessionDatabase       // clock sessions; used to keep the active-civ pin aligned with the running shift
}

// UserHandler returns a user given a userID
func (u User) UserHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["user_id"]

	zap.S().Debugf("user_id: %v", commID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	dbResp := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": cID}).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// UsersFindAllHandler runs a mongo find{} query to find all
// Deprecated: this is not used in the codebase
func (u User) UsersFindAllHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["active_community_id"]

	zap.S().Debugf("active_community_id: %v", commID)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	cursor, err := u.DB.Find(ctx, bson.M{"user.activeCommunity": commID})
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	// Because the frontend requires that the data elements inside models.User exist, if
	// len == 0 then we will just return an empty data object
	if len(users) == 0 {
		users = []models.User{}
	}
	b, err := json.Marshal(users)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// FetchUsersByIdsHandler returns an array of users given an array of user IDs
func (u User) FetchUsersByIdsHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserIDs []string `json:"userIds"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	var objectIDs []primitive.ObjectID
	for _, id := range requestBody.UserIDs {
		objID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
			return
		}
		objectIDs = append(objectIDs, objID)
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": bson.M{"$in": objectIDs}}
	cursor, err := u.DB.Find(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to fetch users", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	if len(users) == 0 {
		users = []models.User{}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": users,
	})
}

// UserLoginHandler returns a session token for a user
func (u User) UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	email, password, ok := r.BasicAuth()
	if ok {
		// Use request context with timeout for proper trace tracking and timeout handling
		ctx, cancel := api.WithQueryTimeout(r.Context())
		defer cancel()

		usernameHash := sha256.Sum256([]byte(email))

		// fetch email & pass from db
		dbEmailResp := models.User{}
		err := u.DB.FindOne(ctx, bson.M{"user.email": email}).Decode(&dbEmailResp)
		if err != nil {
			config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
			return
		}

		expectedUsernameHash := sha256.Sum256([]byte(dbEmailResp.Details.Email))
		usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1

		err = bcrypt.CompareHashAndPassword([]byte(dbEmailResp.Details.Password), []byte(password))
		if err != nil {
			config.ErrorStatus("failed to compare password", http.StatusUnauthorized, w, err)
			return
		}

		if usernameMatch {
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// Note: Removed WWW-Authenticate header to prevent iOS from hanging on 401 responses
	http.Error(w, "Unauthorized", http.StatusUnauthorized)

}

// UserCreateHandler creates a user
func (u User) UserCreateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var user models.UserDetails
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Normalize email to lowercase
	user.Email = strings.TrimSpace(strings.ToLower(user.Email))

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// check if the user already exists
	existingUser := models.User{}
	_ = u.DB.FindOne(ctx, bson.M{"user.email": user.Email}).Decode(&existingUser)
	if existingUser.ID != "" {
		config.ErrorStatus("email already exists", http.StatusConflict, w, fmt.Errorf("duplicate email"))
		return
	}

	// hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
	if err != nil {
		config.ErrorStatus("failed to hash password", http.StatusInternalServerError, w, err)
		return
	}
	user.Password = string(hashedPassword)
	user.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// insert the user
	_, err = u.DB.InsertOne(ctx, user)
	if err != nil {
		config.ErrorStatus("failed to insert user", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)

}

// UserCheckEmailHandler checks if an email exists using POST
func (u User) UserCheckEmailHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var user models.UserDetails
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Normalize email to lowercase
	user.Email = strings.TrimSpace(strings.ToLower(user.Email))

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// check if the user already exists
	existingUser := models.User{}
	_ = u.DB.FindOne(ctx, bson.M{"user.email": user.Email}).Decode(&existingUser)
	if existingUser.ID != "" {
		config.ErrorStatus("email already exists", http.StatusConflict, w, fmt.Errorf("duplicate email"))
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UsersDiscoverPeopleHandler returns a list of users that we suggest to the user to follow
// TODO: Implement proper discover people logic - this is currently hardcoded to return random users
func (u User) UsersDiscoverPeopleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Simple pipeline to get a few random users
	pipeline := []bson.M{
		{
			"$sample": bson.M{"size": 3}, // Get 3 random users
		},
	}

	// Execute the aggregation
	cursor, err := u.DB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to get discover people recommendations", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	// Decode the results
	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	// Return the results
	if len(users) == 0 {
		users = []models.User{}
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"users": users,
	})
}

// UsersLastAccessedCommunityHandler returns the last accessed community details for a user
func (u User) UsersLastAccessedCommunityHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		config.ErrorStatus("query param userId is required", http.StatusBadRequest, w, fmt.Errorf("query param userId is required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid userId", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the user by userId
	user := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to get user by userId", http.StatusNotFound, w, err)
		return
	}

	// Get the last accessed community
	lastAccessedCommunity := user.Details.LastAccessedCommunity
	if lastAccessedCommunity == (models.LastAccessedCommunity{}) || lastAccessedCommunity.CommunityID == "" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
		return
	}

	cID, err := primitive.ObjectIDFromHex(lastAccessedCommunity.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	community, err := u.CDB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Marshal the response
	b, err := json.Marshal(community)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// setActiveCivilianRequest carries the upsert payload for
// PUT /api/v2/user/active-civilian.
type setActiveCivilianRequest struct {
	UserID      string `json:"userId"`
	CommunityID string `json:"communityId"`
	CivilianID  string `json:"civilianId"`
}

// GetActiveCivilianHandler returns the active civilian the user has picked
// for a given community, or an empty body when none is set. Shared with the
// Discord bot's /set-active-civilian — both surfaces read/write the same row.
func (u User) GetActiveCivilianHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	userID := r.URL.Query().Get("userId")
	communityID := r.URL.Query().Get("communityId")
	if userID == "" || communityID == "" {
		config.ErrorStatus("userId and communityId are required", http.StatusBadRequest, w, nil)
		return
	}
	if u.ACDB == nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("null"))
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()
	doc, err := u.ACDB.FindOne(ctx, bson.M{"userId": userID, "communityId": communityID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("null"))
			return
		}
		config.ErrorStatus("failed to load active civilian", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(doc)
}

// SetActiveCivilianHandler upserts the user's active civilian for a given
// community. Web wallet's "Set as active" button and bot /set-active-civilian
// both hit this. Idempotent (same payload twice = same end state).
func (u User) SetActiveCivilianHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var req setActiveCivilianRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}
	if req.UserID == "" || req.CommunityID == "" || req.CivilianID == "" {
		config.ErrorStatus("userId, communityId, civilianId are required", http.StatusBadRequest, w, nil)
		return
	}
	if u.ACDB == nil {
		config.ErrorStatus("active-civilian store not configured", http.StatusInternalServerError, w, nil)
		return
	}
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Coupling rule: the active civilian must match whichever civilian is
	// currently on the clock for this user (if any). Downstream UI (wallet
	// cards, job lists, clock-in/out modals, the bot's defaults) all read
	// the active pin and assume it's the running-shift civilian — letting
	// the two diverge desyncs every surface. If a different civilian is
	// already on the clock, refuse the swap and surface the conflict the
	// same shape as ClockInHandler does (409 + existing session body) so
	// every caller can render a consistent "clock out first" prompt.
	if u.SDB != nil {
		if existing, _ := u.SDB.FindOne(ctx, bson.M{"status": "active", "userId": req.UserID}); existing != nil {
			if existing.CivilianID != "" && existing.CivilianID != req.CivilianID {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error":   "active_session_with_different_civilian",
					"message": "another civilian on this account is currently on the clock; clock them out before changing the active civilian",
					"session": existing,
				})
				return
			}
		}
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	err := u.ACDB.UpdateOne(ctx,
		bson.M{"userId": req.UserID, "communityId": req.CommunityID},
		bson.M{"$set": bson.M{
			"userId":      req.UserID,
			"communityId": req.CommunityID,
			"civilianId":  req.CivilianID,
			"updatedAt":   now,
		}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		config.ErrorStatus("failed to set active civilian", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"userId":      req.UserID,
		"communityId": req.CommunityID,
		"civilianId":  req.CivilianID,
		"updatedAt":   now,
	})
}

// UserFriendsHandler returns a list of friends for a user with pagination
func (u User) UserFriendsHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		config.ErrorStatus("query param userId is required", http.StatusBadRequest, w, fmt.Errorf("query param userId is required"))
		return
	}

	limitParam := r.URL.Query().Get("limit")
	pageParam := r.URL.Query().Get("page")

	limit := 10 // default limit
	page := 1   // default page

	if limitParam != "" {
		if l, err := strconv.Atoi(limitParam); err == nil {
			limit = l
		}
	}

	if pageParam != "" {
		if p, err := strconv.Atoi(pageParam); err == nil {
			page = p
		}
	}

	zap.S().Debugf("userId: %v, limit: %v, page: %v", userID, limit, page)
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid userId", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		// Return an empty array if no user is found
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
		return
	}

	friends := dbResp.Details.Friends
	if friends == nil {
		friends = []models.Friend{}
	}

	start := (page - 1) * limit
	end := start + limit

	if start > len(friends) {
		start = len(friends)
	}
	if end > len(friends) {
		end = len(friends)
	}

	paginatedFriends := friends[start:end]

	// CRITICAL OPTIMIZATION: Batch fetch all friend details in one query instead of N+1 queries
	if len(paginatedFriends) == 0 {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
		return
	}

	// Collect all friend IDs
	friendIDs := make([]primitive.ObjectID, 0, len(paginatedFriends))
	friendIDMap := make(map[string]int) // Map friendID string to index in paginatedFriends
	for i, friend := range paginatedFriends {
		fID, err := primitive.ObjectIDFromHex(friend.FriendID)
		if err != nil {
			continue
		}
		friendIDs = append(friendIDs, fID)
		friendIDMap[friend.FriendID] = i
	}

	// Batch fetch all friends in one query
	friendDetailsMap := make(map[string]models.User)
	if len(friendIDs) > 0 {
		friendFilter := bson.M{"_id": bson.M{"$in": friendIDs}}
		cursor, err := u.DB.Find(ctx, friendFilter)
		if err == nil {
			var friendsList []models.User
			if err := cursor.All(ctx, &friendsList); err == nil {
				for _, friendDetail := range friendsList {
					friendDetailsMap[friendDetail.ID] = friendDetail
				}
			}
			cursor.Close(ctx)
		}
	}

	// Build response using batch-fetched data
	var detailedFriends []map[string]interface{}
	for _, friend := range paginatedFriends {
		friendDetails, exists := friendDetailsMap[friend.FriendID]
		if !exists {
			continue // Skip if friend not found
		}

		detailedFriend := map[string]interface{}{
			"friend":  friend,
			"details": friendDetails,
		}
		detailedFriends = append(detailedFriends, detailedFriend)
	}

	b, err := json.Marshal(detailedFriends)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// AddFriendHandler adds a friend to a user
func (u User) AddFriendHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	if userID == "" {
		config.ErrorStatus("query param userId is required", http.StatusBadRequest, w, fmt.Errorf("query param userId is required"))
		return
	}

	var friend struct {
		FriendID string `json:"friend_id"`
	}
	err := json.NewDecoder(r.Body).Decode(&friend)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid userId", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Retrieve the user's details
	filter := bson.M{"_id": uID}
	user := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's friends", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the friends array is nil or empty
	if user.Details.Friends == nil || len(user.Details.Friends) == 0 {
		newFriend := models.Friend{
			FriendID:  friend.FriendID,
			Status:    "pending",
			CreatedAt: time.Now(),
		}
		update := bson.M{
			"$set": bson.M{"user.friends": []models.Friend{newFriend}},
		}
		_, err = u.DB.UpdateOne(ctx, filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's friends", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Check if the friend already exists
		existingFriend := models.User{}
		err := u.DB.FindOne(ctx, bson.M{"_id": uID, "user.friends.friend_id": friend.FriendID}).Decode(&existingFriend)
		if err == nil && existingFriend.Details.Friends != nil {
			for _, f := range existingFriend.Details.Friends {
				if f.FriendID == friend.FriendID {
					if f.Status == "pending" {
						config.ErrorStatus("friend request is already pending", http.StatusConflict, w, fmt.Errorf("friend request is already pending"))
						return
					} else if f.Status == "approved" {
						config.ErrorStatus("friend is already approved", http.StatusConflict, w, fmt.Errorf("friend is already approved"))
						return
					}
				}
			}
		}

		newFriend := models.Friend{
			FriendID:  friend.FriendID,
			Status:    "pending",
			CreatedAt: time.Now(),
		}

		update := bson.M{"$push": bson.M{"user.friends": newFriend}}
		opts := options.Update().SetUpsert(false)

		_, err = u.DB.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			config.ErrorStatus("failed to add friend", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "friend added successfully"}`))
}

// AddNotificationHandler adds a notification to a user
func (u User) AddNotificationHandler(w http.ResponseWriter, r *http.Request) {
	notification := models.Notification{}
	err := json.NewDecoder(r.Body).Decode(&notification)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Perform blocked user check only if the notification type is "friend_request"
	if notification.Type == "friend_request" {
		nID, err := primitive.ObjectIDFromHex(notification.SentToID)
		if err != nil {
			config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
			return
		}

		filter := bson.M{"_id": nID}
		dbResp := models.User{}
		err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
		if err != nil {
			config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
			return
		}

		for _, friend := range dbResp.Details.Friends {
			if friend.FriendID == notification.SentFromID && friend.Status == "blocked" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"message": "notification created successfully"}`))
				return
			}
		}
	}

	// Create the new notification
	newNotification := models.Notification{
		ID:         primitive.NewObjectID().Hex(),
		SentFromID: notification.SentFromID,
		SentToID:   notification.SentToID,
		Type:       notification.Type,
		Message:    notification.Message,
		Data1:      notification.Data1,
		Data2:      notification.Data2,
		Data3:      notification.Data3,
		Data4:      notification.Data4,
		Seen:       false,
		CreatedAt:  time.Now(),
	}

	nID, err := primitive.ObjectIDFromHex(notification.SentToID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": nID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Deduplicate join_request notifications: skip if an unseen one already exists
	// from the same sender for the same community+department to the same recipient.
	// Data3 distinguishes department requests (departmentId) from community requests (empty).
	if notification.Type == "join_request" {
		for _, existing := range dbResp.Details.Notifications {
			if existing.Type == "join_request" &&
				existing.SentFromID == notification.SentFromID &&
				existing.Data1 == notification.Data1 &&
				existing.Data3 == notification.Data3 &&
				!existing.Seen {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"message": "notification already exists"}`))
				return
			}
		}
	}

	// Look up sender details for the WebSocket payload
	var senderUsername, senderProfilePic string
	if notification.SentFromID != "" {
		senderOID, senderErr := primitive.ObjectIDFromHex(notification.SentFromID)
		if senderErr == nil {
			var sender models.User
			if err := u.DB.FindOne(ctx, bson.M{"_id": senderOID}).Decode(&sender); err == nil {
				senderUsername = sender.Details.Username
				senderProfilePic = sender.Details.ProfilePicture
			}
		}
	}

	// Build enriched payload for WebSocket (includes sender details the toast needs)
	wsPayload := map[string]interface{}{
		"_id":              newNotification.ID,
		"sentFromID":       newNotification.SentFromID,
		"sentToID":         newNotification.SentToID,
		"type":             newNotification.Type,
		"message":          newNotification.Message,
		"data1":            newNotification.Data1,
		"data2":            newNotification.Data2,
		"data3":            newNotification.Data3,
		"data4":            newNotification.Data4,
		"seen":             newNotification.Seen,
		"createdAt":        newNotification.CreatedAt,
		"senderUsername":    senderUsername,
		"senderProfilePic": senderProfilePic,
	}

	// Check if the notifications array is nil or empty
	if dbResp.Details.Notifications == nil || len(dbResp.Details.Notifications) == 0 {
		update := bson.M{
			"$set": bson.M{"user.notifications": []models.Notification{newNotification}},
		}
		_, err = u.DB.UpdateOne(ctx, filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's notifications", http.StatusInternalServerError, w, err)
			return
		}
		sendNotificationToUser(notification.SentToID, wsPayload)
	} else {
		update := bson.M{"$push": bson.M{"user.notifications": newNotification}}
		opts := options.Update().SetUpsert(false)

		_, err = u.DB.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			config.ErrorStatus("failed to create notification", http.StatusInternalServerError, w, err)
			return
		}
		sendNotificationToUser(notification.SentToID, wsPayload)
	}

	// Send push notification in the background (non-blocking)
	go u.sendNotificationPush(notification.SentToID, newNotification, senderUsername)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "notification created successfully"}`))
}

// sendNotificationPush sends an Expo push notification for a regular in-app notification.
// It checks the user's notification preferences before sending.
func (u User) sendNotificationPush(recipientID string, notif models.Notification, senderUsername string) {
	ctx := context.Background()

	// Look up push tokens for the recipient
	tokens, err := u.PTDB.Find(ctx, bson.M{"userId": recipientID})
	if err != nil {
		zap.S().Errorf("sendNotificationPush: failed to fetch push tokens for user %s: %v", recipientID, err)
		return
	}
	if len(tokens) == 0 {
		return
	}

	// Check notification preferences (default to all-enabled if not set)
	prefs := models.DefaultNotificationPreferences()
	if u.UPDB != nil {
		var userPrefs models.UserPreferences
		if err := u.UPDB.FindOne(ctx, bson.M{"userId": recipientID}).Decode(&userPrefs); err == nil {
			p := userPrefs.NotificationPreferences
			// Only use stored prefs if at least one field has been explicitly set
			if p.AllNotifications || p.Friends || p.CommunityJoins || p.DepartmentJoins || p.PanicAlerts || p.General {
				prefs = p
			}
		}
	}

	// Master toggle check
	if !prefs.AllNotifications {
		return
	}

	// Category check
	switch notif.Type {
	case "friend_request":
		if !prefs.Friends {
			return
		}
	case "join_request":
		if notif.Data3 != "" {
			// Department join request
			if !prefs.DepartmentJoins {
				return
			}
		} else {
			// Community join request
			if !prefs.CommunityJoins {
				return
			}
		}
	default:
		if !prefs.General {
			return
		}
	}

	// Build title and body based on notification type
	var title, body string
	switch notif.Type {
	case "friend_request":
		title = "Friend Request"
		body = senderUsername + " sent you a friend request"
	case "join_request":
		if notif.Data3 != "" {
			title = "Department Join Request"
			body = senderUsername + " wants to join " + notif.Data4 + " in " + notif.Data2
		} else {
			title = "Community Join Request"
			body = senderUsername + " wants to join " + notif.Data2
		}
	default:
		title = "LPC Notification"
		body = notif.Message
	}

	data := map[string]interface{}{
		"type":           notif.Type,
		"notificationId": notif.ID,
		"screen":         "Notifications",
	}
	if notif.Data1 != "" {
		data["communityId"] = notif.Data1
	}

	var tokenStrings []string
	for _, t := range tokens {
		tokenStrings = append(tokenStrings, t.Token)
	}

	if err := SendExpoPushNotifications(tokenStrings, title, body, data); err != nil {
		zap.S().Errorf("sendNotificationPush: failed to send push for user %s: %v", recipientID, err)
	}
}

// GetUserNotificationsHandlerV2 returns all notifications for a user with pagination
func (u User) GetUserNotificationsHandlerV2(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["user_id"]

	if userID == "" {
		config.ErrorStatus("user_id is required", http.StatusBadRequest, w, fmt.Errorf("user_id is required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, fmt.Errorf("failed to get objectID from Hex: %w", err))
		return
	}

	// Parse pagination parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 10 // Default limit
	if limitStr != "" {
		limit, err = strconv.Atoi(limitStr)
		if err != nil || limit <= 0 || limit > 100 {
			config.ErrorStatus("invalid limit parameter", http.StatusBadRequest, w, fmt.Errorf("invalid limit: %s", limitStr))
			return
		}
	}

	pageStr := r.URL.Query().Get("page")
	page := 1 // Default page
	if pageStr != "" {
		page, err = strconv.Atoi(pageStr)
		if err != nil {
			config.ErrorStatus("invalid page parameter", http.StatusBadRequest, w, fmt.Errorf("invalid page: %s", pageStr))
			return
		}
		if page < 1 {
			page = 1
		}
	}
	skip := (page - 1) * limit

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch user notifications
	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to fetch user notifications", http.StatusInternalServerError, w, fmt.Errorf("failed to fetch user notifications: %w", err))
		return
	}

	notifications := dbResp.Details.Notifications
	if notifications == nil {
		notifications = []models.Notification{}
	}

	// Sort notifications by createdAt descending
	sortedNotifications := make([]models.Notification, len(notifications))
	copy(sortedNotifications, notifications)
	sort.Slice(sortedNotifications, func(i, j int) bool {
		var timeI, timeJ time.Time
		switch v := sortedNotifications[i].CreatedAt.(type) {
		case string:
			timeI, _ = time.Parse(time.RFC3339, v)
		case time.Time:
			timeI = v
		case primitive.DateTime:
			timeI = v.Time()
		}
		switch v := sortedNotifications[j].CreatedAt.(type) {
		case string:
			timeJ, _ = time.Parse(time.RFC3339, v)
		case time.Time:
			timeJ = v
		case primitive.DateTime:
			timeJ = v.Time()
		}
		return timeI.After(timeJ)
	})

	// Early exit if skip exceeds total notifications
	if skip >= len(sortedNotifications) {
		response := map[string]interface{}{
			"notifications": []map[string]interface{}{},
			"page":          page,
			"limit":         limit,
			"total":         len(sortedNotifications),
			"unseenCount":   0,
		}
		responseBody, err := json.Marshal(response)
		if err != nil {
			config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, fmt.Errorf("failed to marshal response: %w", err))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(responseBody)
		return
	}

	// Calculate total unseen notifications
	totalCount := len(sortedNotifications)
	unseenCount := 0
	for _, notification := range sortedNotifications {
		if !notification.Seen {
			unseenCount++
		}
	}

	// Apply pagination
	start := skip
	end := skip + limit
	if end > len(sortedNotifications) {
		end = len(sortedNotifications)
	}
	paginatedNotifications := sortedNotifications[start:end]

	// OPTIMIZATION: Batch fetch all senders in a single query to avoid N+1 queries
	senderIDs := make([]primitive.ObjectID, 0, len(paginatedNotifications))
	for _, notification := range paginatedNotifications {
		senderID, err := primitive.ObjectIDFromHex(notification.SentFromID)
		if err != nil {
			zap.S().Warnw("invalid sender ID in notification", "notificationID", notification.ID, "sentFromID", notification.SentFromID, "error", err)
			continue
		}
		senderIDs = append(senderIDs, senderID)
	}

	// Batch fetch all senders
	senders := make(map[primitive.ObjectID]models.User)
	if len(senderIDs) > 0 {
		cursor, err := u.DB.Find(ctx, bson.M{"_id": bson.M{"$in": senderIDs}})
		if err != nil {
			zap.S().Errorw("failed to batch fetch senders", "error", err, "senderIDs", senderIDs)
			config.ErrorStatus("failed to fetch senders", http.StatusInternalServerError, w, err)
			return
		}
		defer cursor.Close(ctx)

		var senderList []models.User
		if err = cursor.All(ctx, &senderList); err != nil {
			zap.S().Errorw("failed to decode senders", "error", err)
			config.ErrorStatus("failed to decode senders", http.StatusInternalServerError, w, err)
			return
		}

		// Build map for quick lookup (User.ID is a string, convert to ObjectID for map key)
		for _, sender := range senderList {
			senderObjID, err := primitive.ObjectIDFromHex(sender.ID)
			if err != nil {
				zap.S().Warnw("invalid sender ID", "senderID", sender.ID, "error", err)
				continue
			}
			senders[senderObjID] = sender
		}
	}

	var detailedNotifications []map[string]interface{}
	var invalidNotificationIDs []string // Notification IDs are strings

	for _, notification := range paginatedNotifications {
		senderID, err := primitive.ObjectIDFromHex(notification.SentFromID)
		if err != nil {
			zap.S().Warnw("invalid sender ID in notification", "notificationID", notification.ID, "sentFromID", notification.SentFromID, "error", err)
			invalidNotificationIDs = append(invalidNotificationIDs, notification.ID)
			continue
		}

		sender, senderExists := senders[senderID]
		if !senderExists {
			// Sender doesn't exist, mark for removal
			invalidNotificationIDs = append(invalidNotificationIDs, notification.ID)
			// Decrement the total notifications count
			totalCount--
			// Decrement the unseen count if the notification was unseen
			if !notification.Seen {
				unseenCount--
			}
			continue
		}

		// Calculate timeAgo
		now := time.Now()
		var createdAtTime time.Time
		if createdAtStr, ok := notification.CreatedAt.(string); ok {
			parsedTime, err := time.Parse(time.RFC3339, createdAtStr)
			if err != nil {
				config.ErrorStatus("failed to parse createdAt", http.StatusInternalServerError, w, fmt.Errorf("failed to parse createdAt: %w", err))
				return
			}
			createdAtTime = parsedTime
		} else {
			switch v := notification.CreatedAt.(type) {
			case time.Time:
				createdAtTime = v
			case primitive.DateTime:
				createdAtTime = v.Time()
			default:
				config.ErrorStatus("invalid createdAt time", http.StatusInternalServerError, w, fmt.Errorf("invalid createdAt time"))
				return
			}
		}
		duration := now.Sub(createdAtTime)
		var timeAgo string
		seconds := duration.Seconds()
		minutes := duration.Minutes()
		hours := duration.Hours()
		if seconds < 60 {
			timeAgo = fmt.Sprintf("%.0f seconds ago", seconds)
		} else if minutes < 60 {
			timeAgo = fmt.Sprintf("%.0f minutes ago", minutes)
		} else if hours <= 24 {
			timeAgo = fmt.Sprintf("%.0f hours ago", hours)
		} else if hours <= 24*365 {
			days := hours / 24
			timeAgo = fmt.Sprintf("%.0f days ago", days)
		} else {
			years := hours / (24 * 365)
			timeAgo = fmt.Sprintf("%.0f years ago", years)
		}

		// Handle sender details with fallback
		senderName := sender.Details.Name
		senderUsername := sender.Details.Username
		senderProfilePicture := sender.Details.ProfilePicture

		detailedNotification := map[string]interface{}{
			"notificationId":   notification.ID,
			"sentFromID":       notification.SentFromID,
			"sentToID":         notification.SentToID,
			"type":             notification.Type,
			"message":          notification.Message,
			"data1":            notification.Data1,
			"data2":            notification.Data2,
			"data3":            notification.Data3,
			"data4":            notification.Data4,
			"seen":             notification.Seen,
			"createdAt":        notification.CreatedAt,
			"timeAgo":          timeAgo,
			"senderName":       senderName,
			"senderUsername":   senderUsername,
			"senderProfilePic": senderProfilePicture,
		}
		detailedNotifications = append(detailedNotifications, detailedNotification)
	}

	// Remove invalid notifications in batch (if any)
	if len(invalidNotificationIDs) > 0 {
		filter := bson.M{"_id": uID}
		// Use $in with string IDs for notification removal
		update := bson.M{"$pull": bson.M{"user.notifications": bson.M{"_id": bson.M{"$in": invalidNotificationIDs}}}}
		_, err = u.DB.UpdateOne(ctx, filter, update)
		if err != nil {
			zap.S().Warnw("failed to remove invalid notifications", "error", err, "notificationIDs", invalidNotificationIDs)
			// Don't fail the request, just log the warning
		}
	}

	// Prepare the response
	response := map[string]interface{}{
		"notifications": detailedNotifications,
		"page":          page,
		"limit":         limit,
		"total":         totalCount,
		"unseenCount":   unseenCount,
	}

	responseBody, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, fmt.Errorf("failed to marshal response: %w", err))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// GetUserNotificationsHandler returns all notifications for a user
// Deprecated: use GetUserNotificationsHandlerV2 instead
func (u User) GetUserNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["user_id"]

	if userID == "" {
		config.ErrorStatus("user_id is required", http.StatusBadRequest, w, fmt.Errorf("user_id is required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to fetch user notifications", http.StatusInternalServerError, w, err)
		return
	}

	notifications := dbResp.Details.Notifications
	if notifications == nil {
		notifications = []models.Notification{}
	}

	var detailedNotifications []map[string]interface{}
	for _, notification := range notifications {
		senderID, err := primitive.ObjectIDFromHex(notification.SentFromID)
		if err != nil {
			config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
			return
		}

		sender := models.User{}
		err = u.DB.FindOne(ctx, bson.M{"_id": senderID}).Decode(&sender)
		if err != nil {
			// Skip this notification if the sender is not found
			continue
		}

		// Calculate timeAgo
		now := time.Now()
		var createdAtTime time.Time

		// Check if CreatedAt is a string and parse it
		if createdAtStr, ok := notification.CreatedAt.(string); ok {
			parsedTime, err := time.Parse(time.RFC3339, createdAtStr)
			if err != nil {
				config.ErrorStatus("failed to parse createdAt", http.StatusInternalServerError, w, err)
				return
			}
			createdAtTime = parsedTime
		} else {
			switch v := notification.CreatedAt.(type) {
			case time.Time:
				createdAtTime = v
			case primitive.DateTime:
				createdAtTime = v.Time()
			default:
				config.ErrorStatus("invalid last accessed time", http.StatusInternalServerError, w, fmt.Errorf("invalid last accessed time"))
				return
			}
		}
		duration := now.Sub(createdAtTime)
		var timeAgo string
		seconds := duration.Seconds()
		minutes := duration.Minutes()
		hours := duration.Hours()
		if seconds < 60 {
			timeAgo = fmt.Sprintf("%.0f seconds ago", seconds)
		} else if minutes < 60 {
			timeAgo = fmt.Sprintf("%.0f minutes ago", minutes)
		} else if hours <= 24 {
			timeAgo = fmt.Sprintf("%.0f hours ago", hours)
		} else if hours <= 24*365 {
			days := hours / 24
			timeAgo = fmt.Sprintf("%.0f days ago", days)
		} else {
			years := hours / (24 * 365)
			timeAgo = fmt.Sprintf("%.0f years ago", years)
		}

		detailedNotification := map[string]interface{}{
			"notificationId":       notification.ID,
			"friendId":             notification.SentFromID,
			"type":                 notification.Type,
			"message":              notification.Message,
			"data1":                notification.Data1,
			"data2":                notification.Data2,
			"data3":                notification.Data3,
			"data4":                notification.Data4,
			"seen":                 notification.Seen,
			"createdAt":            notification.CreatedAt,
			"senderName":           sender.Details.Name,
			"senderUsername":       sender.Details.Username,
			"senderProfilePicture": sender.Details.ProfilePicture,
			"timeAgo":              timeAgo,
		}
		detailedNotifications = append(detailedNotifications, detailedNotification)
	}

	responseBody, err := json.Marshal(detailedNotifications)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBody)
}

// UpdateFriendStatusHandler updates the status of a friend
func (u User) UpdateFriendStatusHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["user_id"]

	if userID == "" {
		config.ErrorStatus("user_id is required", http.StatusBadRequest, w, fmt.Errorf("user_id is required"))
		return
	}

	var updateRequest struct {
		FriendID string `json:"friendId"`
		Status   string `json:"status"`
	}
	err := json.NewDecoder(r.Body).Decode(&updateRequest)
	if err != nil {
		config.ErrorStatus("failed to decode request", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	fID, err := primitive.ObjectIDFromHex(updateRequest.FriendID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Update the user's friend status
	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	friends := dbResp.Details.Friends
	if friends == nil {
		config.ErrorStatus("no friends found", http.StatusNotFound, w, fmt.Errorf("no friends found"))
		return
	}

	friendFound := false
	for i, friend := range friends {
		if friend.FriendID == updateRequest.FriendID {
			friends[i].Status = updateRequest.Status
			friendFound = true
			break
		}
	}

	if !friendFound {
		config.ErrorStatus("friend not found", http.StatusNotFound, w, fmt.Errorf("friend not found"))
		return
	}

	update := bson.M{"$set": bson.M{"user.friends": friends}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update friend status", http.StatusInternalServerError, w, err)
		return
	}

	// Update the friend's friend status
	friendFilter := bson.M{"_id": fID}
	friendResp := models.User{}
	err = u.DB.FindOne(ctx, friendFilter).Decode(&friendResp)
	if err != nil {
		config.ErrorStatus("failed to get friend by ID", http.StatusNotFound, w, err)
		return
	}

	friendFriends := friendResp.Details.Friends
	if friendFriends == nil {
		friendFriends = []models.Friend{}
	}

	userFound := false
	for i, friend := range friendFriends {
		if friend.FriendID == userID {
			friendFriends[i].Status = updateRequest.Status
			userFound = true
			break
		}
	}

	if !userFound {
		newFriend := models.Friend{
			FriendID:  userID,
			Status:    updateRequest.Status,
			CreatedAt: time.Now(),
		}
		friendFriends = append(friendFriends, newFriend)
	}

	friendUpdate := bson.M{"$set": bson.M{"user.friends": friendFriends}}
	_, err = u.DB.UpdateOne(ctx, friendFilter, friendUpdate)
	if err != nil {
		config.ErrorStatus("failed to update friend's friend status", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "friend status updated successfully"}`))
}

// MarkNotificationAsReadHandler marks a notification as read
func (u User) MarkNotificationAsReadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["user_id"]
	notificationID := vars["notification_id"]

	if userID == "" || notificationID == "" {
		config.ErrorStatus("user_id and notification_id are required", http.StatusBadRequest, w, fmt.Errorf("user_id and notification_id are required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	notifications := dbResp.Details.Notifications
	if notifications == nil {
		config.ErrorStatus("no notifications found", http.StatusNotFound, w, fmt.Errorf("no notifications found"))
		return
	}

	notificationFound := false
	for i, notification := range notifications {
		if notification.ID == notificationID {
			notifications[i].Seen = true
			notificationFound = true
			break
		}
	}

	if !notificationFound {
		config.ErrorStatus("notification not found", http.StatusNotFound, w, fmt.Errorf("notification not found"))
		return
	}

	update := bson.M{"$set": bson.M{"user.notifications": notifications}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to mark notification as read", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "notification marked as read successfully"}`))
}

// DeleteNotificationHandler deletes a notification
func (u User) DeleteNotificationHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["user_id"]
	notificationID := vars["notification_id"]

	if userID == "" || notificationID == "" {
		config.ErrorStatus("user_id and notification_id are required", http.StatusBadRequest, w, fmt.Errorf("user_id and notification_id are required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	notifications := dbResp.Details.Notifications
	if notifications == nil {
		config.ErrorStatus("no notifications found", http.StatusNotFound, w, fmt.Errorf("no notifications found"))
		return
	}

	notificationFound := false
	for i, notification := range notifications {
		if notification.ID == notificationID {
			notifications = append(notifications[:i], notifications[i+1:]...)
			notificationFound = true
			break
		}
	}

	if !notificationFound {
		config.ErrorStatus("notification not found", http.StatusNotFound, w, fmt.Errorf("notification not found"))
		return
	}

	update := bson.M{"$set": bson.M{"user.notifications": notifications}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete notification", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "notification deleted successfully"}`))
}

// fetchUserFriendsByID returns a list of friends for a user
func (u User) fetchUserFriendsByID(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["userId"]

	if userID == "" {
		config.ErrorStatus("userId is required", http.StatusBadRequest, w, fmt.Errorf("userId is required"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(ctx, filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	friends := dbResp.Details.Friends
	if friends == nil {
		friends = []models.Friend{}
	}

	var approvedFriends []models.Friend
	for _, friend := range friends {
		if friend.Status == "approved" {
			approvedFriends = append(approvedFriends, friend)
		}
	}

	response := map[string]interface{}{
		"count": len(approvedFriends),
		// "friends": approvedFriends,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// fetchFriendsAndMutualFriendsCount returns the count of mutual friends between two users
func (u User) fetchFriendsAndMutualFriendsCount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	friendID := vars["friend_id"]
	userID := r.URL.Query().Get("userId")

	if friendID == "" || userID == "" {
		config.ErrorStatus("friend_id and userId are required", http.StatusBadRequest, w, fmt.Errorf("friend_id and userId are required"))
		return
	}

	fID, err := primitive.ObjectIDFromHex(friendID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	friendFilter := bson.M{"_id": fID}
	friendResp := models.User{}
	err = u.DB.FindOne(ctx, friendFilter).Decode(&friendResp)
	if err != nil {
		config.ErrorStatus("failed to get friend by ID", http.StatusNotFound, w, err)
		return
	}

	userFilter := bson.M{"_id": uID}
	userResp := models.User{}
	err = u.DB.FindOne(ctx, userFilter).Decode(&userResp)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	friendFriends := friendResp.Details.Friends
	userFriends := userResp.Details.Friends

	if friendFriends == nil {
		friendFriends = []models.Friend{}
	}
	if userFriends == nil {
		userFriends = []models.Friend{}
	}

	var approvedFriendFriends []models.Friend
	for _, friend := range friendFriends {
		if friend.Status == "approved" {
			approvedFriendFriends = append(approvedFriendFriends, friend)
		}
	}

	approvedFriendFriendsCount := len(approvedFriendFriends)

	mutualFriendsCount := 0
	for _, userFriend := range userFriends {
		if userFriend.Status == "approved" {
			for _, friendFriend := range approvedFriendFriends {
				if userFriend.FriendID == friendFriend.FriendID {
					mutualFriendsCount++
					break
				}
			}
		}
	}

	response := map[string]interface{}{
		"approvedFriendFriendsCount": approvedFriendFriendsCount,
		"mutualFriendsCount":         mutualFriendsCount,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// GetUserCommunitiesHandler returns the communities a user is a part of
// Deprecated: use FetchUserCommunitiesHandler instead for better performance, pagination, and filtering
func (u User) GetUserCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}

	// Find the user by ID
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	// Extract the communities from the user
	communities := user.Details.Communities
	if communities == nil {
		communities = []models.UserCommunity{}
	}

	// Parse optional filter query parameter
	filter := r.URL.Query().Get("filter")
	if filter != "" {
		parts := strings.SplitN(filter, ":", 2)
		if len(parts) == 2 {
			filterKey, filterValue := parts[0], parts[1]
			var filteredCommunities []models.UserCommunity
			for _, community := range communities {
				if filterKey == "status" && community.Status == filterValue {
					filteredCommunities = append(filteredCommunities, community)
				}
			}
			communities = filteredCommunities
		}
	}

	// Parse pagination parameters
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Default limit
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default page
	}

	// Apply pagination
	start := (page - 1) * limit
	end := start + limit
	if start > len(communities) {
		start = len(communities)
	}
	if end > len(communities) {
		end = len(communities)
	}
	paginatedCommunities := communities[start:end]

	// Marshal the paginated communities to JSON
	b, err := json.Marshal(paginatedCommunities)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// UpdateLastAccessedCommunityHandler updates the last accessed community for a user
func (u User) UpdateLastAccessedCommunityHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID      string `json:"userId"`
		CommunityID string `json:"communityId"`
		CreatedAt   string `json:"createdAt"`
	}

	// Parse the request body to get the update details
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID and community ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(request.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Convert the createdAt to a primitive.DateTime
	createdAt, err := time.Parse(time.RFC3339, request.CreatedAt)
	if err != nil {
		config.ErrorStatus("failed to parse createdAt", http.StatusBadRequest, w, err)
		return
	}
	createdAtPrimitive := primitive.NewDateTimeFromTime(createdAt)

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Update the user's lastAccessedCommunity details
	filter := bson.M{"_id": uID}
	update := bson.M{"$set": bson.M{
		"user.lastAccessedCommunity.communityID": request.CommunityID,
		"user.lastAccessedCommunity.createdAt":   createdAtPrimitive,
	}}

	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update lastAccessedCommunity", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Last accessed community updated successfully"}`))
}

// GetRandomCommunitiesHandler returns a list of random communities that the user does not belong to
func (u User) GetRandomCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	if limit <= 0 {
		limit = 10 // default limit
	}

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}

	// Find the user by ID
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	// Extract the communities from the user
	userCommunities := user.Details.Communities
	if userCommunities == nil {
		userCommunities = []models.UserCommunity{}
	}

	// Convert community IDs to primitive.ObjectID for communities with status "approved"
	var communityObjectIDs []primitive.ObjectID
	for _, community := range userCommunities {
		if community.Status == "approved" {
			objID, err := primitive.ObjectIDFromHex(community.CommunityID)
			if err != nil {
				// Log error but continue - invalid IDs shouldn't break the query
				zap.S().Warnw("invalid community ID in user communities", "communityID", community.CommunityID, "error", err)
				continue
			}
			communityObjectIDs = append(communityObjectIDs, objID)
		}
	}

	if communityObjectIDs == nil {
		communityObjectIDs = []primitive.ObjectID{}
	}

	// OPTIMIZATION: Use aggregation with $sample for better performance when user has many communities
	// $nin with large arrays can be slow, so we'll sample first then filter
	var communities []models.Community
	
	// If user has many communities, use aggregation with $sample for better performance
	if len(communityObjectIDs) > 50 {
		// Use aggregation pipeline: sample large pool, then filter out user communities
		pipeline := mongo.Pipeline{
			{{"$match", bson.M{"community.visibility": "public"}}},
			{{"$sample", bson.M{"size": limit * 5}}}, // Sample 5x the limit to account for filtering
		}
		
		cursor, err := u.CDB.Aggregate(ctx, pipeline)
		if err != nil {
			zap.S().Errorw("failed to aggregate random communities", "error", err, "userId", userID)
			config.ErrorStatus("failed to find communities", http.StatusInternalServerError, w, err)
			return
		}
		defer cursor.Close(ctx)
		
		var sampledCommunities []models.Community
		if err = cursor.All(ctx, &sampledCommunities); err != nil {
			zap.S().Errorw("failed to decode sampled communities", "error", err, "userId", userID)
			config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
			return
		}
		
		// Filter out user communities
		userCommunityMap := make(map[primitive.ObjectID]bool)
		for _, id := range communityObjectIDs {
			userCommunityMap[id] = true
		}
		
		for _, comm := range sampledCommunities {
			if !userCommunityMap[comm.ID] {
				communities = append(communities, comm)
				if len(communities) >= limit {
					break
				}
			}
		}
	} else {
		// For users with few communities, use direct Find with $nin (more efficient)
		filter := bson.M{
			"_id":                  bson.M{"$nin": communityObjectIDs},
			"community.visibility": "public",
		}
		// Use _id sort instead of $natural for better performance (can use _id index)
		opt := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit)).SetSort(bson.M{"_id": 1})

		cursor, err := u.CDB.Find(ctx, filter, opt)
		if err != nil {
			zap.S().Errorw("failed to find communities", "error", err, "userId", userID)
			config.ErrorStatus("failed to find communities", http.StatusInternalServerError, w, err)
			return
		}
		defer cursor.Close(ctx)

		if err = cursor.All(ctx, &communities); err != nil {
			zap.S().Errorw("failed to decode communities", "error", err, "userId", userID)
			config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Return an empty array if no communities are found
	if len(communities) == 0 {
		communities = []models.Community{}
	}

	// Marshal the communities to JSON
	b, err := json.Marshal(communities)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// AddCommunityToUserHandler adds a community to a user's array of communities
func (u User) AddCommunityToUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body to get the community ID and status
	var requestBody struct {
		CommunityID string `json:"communityId"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, fmt.Errorf("failed to decode body: %w", err))
		return
	}

	// Check for the migration query parameter
	migration := r.URL.Query().Get("migration") == "true"

	// Convert the user ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, fmt.Errorf("failed to get objectID from Hex: %s", userID))
		return
	}

	// Convert the community ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get community objectID from Hex", http.StatusBadRequest, w, fmt.Errorf("failed to get community objectID: %s", requestBody.CommunityID))
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if the community exists
	communityFilter := bson.M{"_id": cID}
	_, err = u.CDB.FindOne(ctx, communityFilter)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			config.ErrorStatus("community does not exist", http.StatusBadRequest, w, fmt.Errorf("community does not exist: %s", requestBody.CommunityID))
		} else {
			config.ErrorStatus("failed to fetch community", http.StatusInternalServerError, w, fmt.Errorf("failed to fetch community: %w", err))
		}
		return
	}

	// Fetch the user document
	filter := bson.M{"_id": uID}
	var user models.User
	err = u.DB.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if migration {
			// Initialize communities array and insert the first record during migration
			newCommunity := models.UserCommunity{
				ID:          primitive.NewObjectID().Hex(),
				CommunityID: requestBody.CommunityID,
				Status:      requestBody.Status,
			}
			update := bson.M{
				"$set": bson.M{"user.communities": []models.UserCommunity{newCommunity}},
			}
			_, err = u.DB.UpdateOne(ctx, filter, update)
			if err != nil {
				config.ErrorStatus("failed to initialize communities during migration", http.StatusInternalServerError, w, fmt.Errorf("failed to initialize communities during migration: %w", err))
				return
			}
			// Increment membersCount for the community during migration
			communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": 1}}
			err = u.CDB.UpdateOne(ctx, communityFilter, communityUpdate)
			if err != nil {
				config.ErrorStatus("failed to increment community membersCount", http.StatusInternalServerError, w, fmt.Errorf("failed to increment community membersCount: %w", err))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message": "Community added successfully during migration"}`))
			return
		}
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, fmt.Errorf("failed to fetch user: %w", err))
		return
	}

	// Check if the communityId already exists in user.communities
	communityExists := false
	var existingCommunity models.UserCommunity
	if user.Details.Communities != nil {
		for _, community := range user.Details.Communities {
			if community.CommunityID == requestBody.CommunityID {
				communityExists = true
				existingCommunity = community
				break
			}
		}
	}

	if communityExists {
		// Update the status of the existing community
		update := bson.M{
			"$set": bson.M{
				"user.communities.$[elem].status": requestBody.Status,
			},
		}
		arrayFilters := options.Update().SetArrayFilters(options.ArrayFilters{
			Filters: []interface{}{
				bson.M{"elem.communityId": requestBody.CommunityID},
			},
		})
		result, err := u.DB.UpdateOne(ctx, filter, update, arrayFilters)
		if err != nil {
			config.ErrorStatus("failed to update community status", http.StatusInternalServerError, w, fmt.Errorf("failed to update community status: %w", err))
			return
		}
		if result.ModifiedCount == 0 {
			if existingCommunity.CommunityID == requestBody.CommunityID {
				config.ErrorStatus("Member already exists", http.StatusConflict, w, fmt.Errorf("member: %v, already has status: %v, for community: %v", userID, requestBody.Status, requestBody.CommunityID))
				return
			}
			config.ErrorStatus("no community status updated", http.StatusBadRequest, w, fmt.Errorf("community status not updated, communityId: %s with status: %s", requestBody.CommunityID, requestBody.Status))
			return
		}

		// Increment membersCount only if transitioning to "approved" from a non-approved state
		if requestBody.Status == "approved" && existingCommunity.Status != "approved" {
			communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": 1}}
			err = u.CDB.UpdateOne(ctx, communityFilter, communityUpdate)
			if err != nil {
				config.ErrorStatus("failed to increment community membersCount", http.StatusInternalServerError, w, fmt.Errorf("failed to increment community membersCount: %w", err))
				return
			}

			// Audit log: member approved (joined via request approval)
			actorID := resolveActorFromRequest(r)
			logAudit(u.ALDB, cID, "member.approved", "member", actorID, resolveActorName(u.DB, actorID), userID, resolveActorName(u.DB, userID), nil)

			// Defense-in-depth for members orphaned before RemoveCommunityFromUser/
			// BanUserFromCommunity cleared department membership: a re-added user
			// could still carry stale entries in community.departments[].members,
			// silently regaining (private) department access and erroring on open.
			// Clear them on re-approval so re-added users start with no department
			// access until explicitly re-granted. Best-effort — never block approval.
			if community, err := u.CDB.FindOne(ctx, communityFilter); err == nil && community != nil {
				for _, dept := range community.Details.Departments {
					deptFilter := bson.M{"_id": cID, "community.departments._id": dept.ID}
					deptUpdate := bson.M{"$pull": bson.M{"community.departments.$.members": bson.M{"userID": userID}}}
					if err := u.CDB.UpdateOne(ctx, deptFilter, deptUpdate); err != nil {
						zap.S().Warnw("failed to clear stale department membership on re-approval",
							"community_id", requestBody.CommunityID, "user_id", userID, "department_id", dept.ID.Hex(), "error", err)
					}
				}
			}
		}

		// Once a join request is resolved (approved or declined), clear the
		// matching community-level join_request notifications from every admin
		// who received one. Notification cleanup was previously client-driven and
		// per-notification, so duplicate or cross-session notifications lingered
		// as "requesting to join" until each surface manually refetched. This
		// mirrors the cleanup already done on request cancellation and on
		// department join-request resolution.
		if requestBody.Status == "approved" || requestBody.Status == "declined" {
			notifMatch := bson.M{
				"type":       "join_request",
				"sentFromID": userID,
				"data1":      requestBody.CommunityID,
				"data3":      "", // community-level request only (department requests carry data3)
			}
			notifFilter := bson.M{"user.notifications": bson.M{"$elemMatch": notifMatch}}
			notifUpdate := bson.M{"$pull": bson.M{"user.notifications": notifMatch}}
			// Best-effort cleanup — never fail the resolution if this errors.
			_, _ = u.DB.UpdateMany(ctx, notifFilter, notifUpdate)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "Community status updated successfully"}`))
		return
	}

	// Add new community with status "pending" (or provided status)
	newCommunity := models.UserCommunity{
		ID:          primitive.NewObjectID().Hex(),
		CommunityID: requestBody.CommunityID,
		Status:      "pending", // Default to pending for new joins
	}
	if requestBody.Status != "" {
		newCommunity.Status = requestBody.Status
	}

	// Atomically ensure communities array is initialized (no-op if already an array).
	_, _ = u.DB.UpdateOne(ctx, bson.M{"_id": uID, "user.communities": nil}, bson.M{"$set": bson.M{"user.communities": bson.A{}}})

	// Atomically append the new community.
	update := bson.M{"$push": bson.M{"user.communities": newCommunity}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to add community", http.StatusInternalServerError, w, fmt.Errorf("failed to add community: %w", err))
		return
	}

	// Increment the membersCount in the community document
	communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": 1}}
	err = u.CDB.UpdateOne(ctx, communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to increment community membersCount", http.StatusInternalServerError, w, fmt.Errorf("failed to increment community membersCount: %w", err))
		return
	}

	// Audit log: member added directly (e.g. via migration or admin action)
	if newCommunity.Status == "approved" {
		actorID := resolveActorFromRequest(r)
		logAudit(u.ALDB, cID, "member.approved", "member", actorID, resolveActorName(u.DB, actorID), userID, resolveActorName(u.DB, userID), nil)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community added successfully"}`))
}

// PendingCommunityRequestHandler handles pending community requests for a user
func (u User) PendingCommunityRequestHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body to get the community ID
	var requestBody struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Check if a community request with this communityId already exists
	var existingCommunity *models.UserCommunity
	if user.Details.Communities != nil && len(user.Details.Communities) > 0 {
		for i := range user.Details.Communities {
			if user.Details.Communities[i].CommunityID == requestBody.CommunityID {
				existingCommunity = &user.Details.Communities[i]
				break
			}
		}
	}

	// Handle based on existing request status
	if existingCommunity != nil {
		switch existingCommunity.Status {
		case "pending":
			// If already pending, return error
			config.ErrorStatus("community request already exists", http.StatusConflict, w, fmt.Errorf("community request already exists"))
			return
		case "approved":
			// Already an approved member — do not reset to pending
			config.ErrorStatus("user is already an approved member of this community", http.StatusConflict, w, fmt.Errorf("user %s already approved for community %s", userID, requestBody.CommunityID))
			return
		case "declined":
			// If declined, update to pending
			filter := bson.M{"_id": uID, "user.communities.communityId": requestBody.CommunityID}
			update := bson.M{
				"$set": bson.M{"user.communities.$.status": "pending"},
			}
			_, err = u.DB.UpdateOne(ctx, filter, update)
			if err != nil {
				config.ErrorStatus("failed to update community request status", http.StatusInternalServerError, w, err)
				return
			}
			// Return success - status updated to pending
		case "blocked":
			// If blocked, silently process but don't change status (just return success)
			// No database update needed
		default:
			// For any other unexpected status, return error rather than silently resetting
			config.ErrorStatus("unexpected community membership status", http.StatusConflict, w, fmt.Errorf("user %s has unexpected status %q for community %s", userID, existingCommunity.Status, requestBody.CommunityID))
			return
		}
	} else {
		// No existing request found, add a new pending request
		pendingRequest := models.UserCommunity{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: requestBody.CommunityID,
			Status:      "pending",
		}

		// Atomically ensure communities array is initialized (no-op if already an array).
		_, _ = u.DB.UpdateOne(ctx, bson.M{"_id": uID, "user.communities": nil}, bson.M{"$set": bson.M{"user.communities": bson.A{}}})

		// Atomically append the pending request.
		update := bson.M{"$push": bson.M{"user.communities": pendingRequest}}
		_, err = u.DB.UpdateOne(ctx, bson.M{"_id": uID}, update)
		if err != nil {
			config.ErrorStatus("failed to add community request", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Pending community request added successfully"}`))
}

// CancelPendingCommunityRequestHandler cancels a pending community join request for a user
func (u User) CancelPendingCommunityRequestHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body to get the community ID
	var requestBody struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	user := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user", http.StatusInternalServerError, w, err)
		return
	}

	// Find the matching community entry and verify it's pending
	var found bool
	if user.Details.Communities != nil {
		for _, c := range user.Details.Communities {
			if c.CommunityID == requestBody.CommunityID && c.Status == "pending" {
				found = true
				break
			}
		}
	}

	if !found {
		config.ErrorStatus("no pending community request found", http.StatusBadRequest, w, fmt.Errorf("no pending community request found for communityId %s", requestBody.CommunityID))
		return
	}

	// Remove the pending entry from the user's communities array
	filter := bson.M{"_id": uID}
	update := bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": requestBody.CommunityID}}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to cancel pending community request", http.StatusInternalServerError, w, err)
		return
	}

	// Clean up join_request notifications sent to admins for this request
	notifFilter := bson.M{
		"user.notifications": bson.M{
			"$elemMatch": bson.M{
				"type":       "join_request",
				"sentFromID": userID,
				"data1":      requestBody.CommunityID,
			},
		},
	}
	notifUpdate := bson.M{
		"$pull": bson.M{
			"user.notifications": bson.M{
				"type":       "join_request",
				"sentFromID": userID,
				"data1":      requestBody.CommunityID,
			},
		},
	}
	// Best-effort cleanup — don't fail the request if notification cleanup fails
	_, _ = u.DB.UpdateMany(ctx, notifFilter, notifUpdate)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Pending community request cancelled successfully"}`))
}

// RemoveCommunityFromUserHandler removes a community from a user's array of communities
func (u User) RemoveCommunityFromUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body to get the community ID
	var requestBody struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID and community ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Check if the community exists in the user's communities array
	userFilter := bson.M{"_id": uID}
	var user models.User
	err = u.DB.FindOne(ctx, userFilter).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the community actually exists in the user's communities array
	communityExists := false
	for _, comm := range user.Details.Communities {
		if comm.CommunityID == requestBody.CommunityID {
			communityExists = true
			break
		}
	}

	if !communityExists {
		config.ErrorStatus("community not found in user's communities", http.StatusBadRequest, w, fmt.Errorf("community %s is not associated with user %s", requestBody.CommunityID, userID))
		return
	}

	// Update the user's communities array to remove the specified community
	userUpdate := bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": requestBody.CommunityID}}}
	_, err = u.DB.UpdateOne(ctx, userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove community from user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Find the community by community ID and decrement the membersCount
	communityFilter := bson.M{"_id": cID}
	communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": -1}}
	err = u.CDB.UpdateOne(ctx, communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to decrement community membersCount", http.StatusInternalServerError, w, err)
		return
	}
	community, err := u.CDB.FindOne(ctx, communityFilter)

	// Iterate through the roles and remove the user ID from the members array
	for _, role := range community.Details.Roles {
		roleFilter := bson.M{"_id": cID, "community.roles._id": role.ID, "community.roles.members": userID}
		roleUpdate := bson.M{"$pull": bson.M{"community.roles.$.members": userID}}
		err := u.CDB.UpdateOne(ctx, roleFilter, roleUpdate)
		if err != nil {
			config.ErrorStatus("failed to remove user from role members", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Remove the user from every department's members. Without this, department
	// membership (especially in private/approval-required departments) survived a
	// community removal — on re-add the user silently regained access without
	// re-approval, and opening such a department errored. Mirror the roles loop
	// above, using the same per-department $pull as RemoveUserFromDepartmentHandler.
	for _, dept := range community.Details.Departments {
		deptFilter := bson.M{"_id": cID, "community.departments._id": dept.ID}
		deptUpdate := bson.M{"$pull": bson.M{"community.departments.$.members": bson.M{"userID": userID}}}
		if err := u.CDB.UpdateOne(ctx, deptFilter, deptUpdate); err != nil {
			config.ErrorStatus("failed to remove user from department members", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Audit log — distinguish kick (admin removed member) vs leave (self-initiated)
	actorID := resolveActorFromRequest(r)
	if actorID != "" && actorID != userID {
		logAudit(u.ALDB, cID, "member.kicked", "member", actorID, resolveActorName(u.DB, actorID), userID, resolveActorName(u.DB, userID), nil)
	} else {
		logAudit(u.ALDB, cID, "member.left", "member", userID, resolveActorName(u.DB, userID), userID, "", nil)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community and roles updated successfully"}`))
}

// BanUserFromCommunityHandler bans a user from a community
func (u User) BanUserFromCommunityHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body to get the community ID
	var requestBody struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID and community ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch user to determine prior community status — we only decrement
	// membersCount if this user was previously "approved" (counted).
	var bannedUser models.User
	if err := u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&bannedUser); err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}
	priorStatus := ""
	for _, uc := range bannedUser.Details.Communities {
		if uc.CommunityID == requestBody.CommunityID {
			priorStatus = uc.Status
			break
		}
	}
	if priorStatus == "" {
		config.ErrorStatus("user is not a member of this community", http.StatusBadRequest, w,
			fmt.Errorf("community %s is not associated with user %s", requestBody.CommunityID, userID))
		return
	}
	if priorStatus == "banned" {
		// Idempotent: already banned.
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "User already banned from community"}`))
		return
	}

	// Update the user's community status to "banned"
	userFilter := bson.M{
		"_id":                          uID,
		"user.communities.communityId": requestBody.CommunityID,
	}
	userUpdate := bson.M{
		"$set": bson.M{
			"user.communities.$.status": "banned",
		},
	}
	_, err = u.DB.UpdateOne(ctx, userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to update community status", http.StatusInternalServerError, w, err)
		return
	}

	// Add the user ID to the community's banList
	communityFilter := bson.M{"_id": cID}
	communityUpdate := bson.M{
		"$addToSet": bson.M{
			"community.banList": userID,
		},
	}
	err = u.CDB.UpdateOne(ctx, communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to update community ban list", http.StatusInternalServerError, w, err)
		return
	}

	// Only decrement membersCount if the user was previously approved (counted).
	// Non-fatal: CommunityHandler's self-healing read will correct any transient
	// drift on the next GET. Don't block a security-critical action on a
	// denormalized-field update failure.
	if priorStatus == "approved" {
		if err := u.CDB.UpdateOne(ctx, communityFilter, bson.M{
			"$inc": bson.M{"community.membersCount": -1},
		}); err != nil {
			zap.S().Warnw("failed to decrement membersCount on ban",
				"community_id", requestBody.CommunityID, "user_id", userID, "error", err)
		}
	}

	// Strip the banned user from every department's members so they can't retain
	// (private) department access — the community-level ban status alone doesn't
	// clear department membership. Best-effort: the ban itself already succeeded,
	// so a transient failure here shouldn't 500 the request (mirrors the
	// membersCount handling above); it's logged for follow-up.
	if community, err := u.CDB.FindOne(ctx, communityFilter); err == nil && community != nil {
		for _, dept := range community.Details.Departments {
			deptFilter := bson.M{"_id": cID, "community.departments._id": dept.ID}
			deptUpdate := bson.M{"$pull": bson.M{"community.departments.$.members": bson.M{"userID": userID}}}
			if err := u.CDB.UpdateOne(ctx, deptFilter, deptUpdate); err != nil {
				zap.S().Warnw("failed to remove banned user from department members",
					"community_id", requestBody.CommunityID, "user_id", userID, "department_id", dept.ID.Hex(), "error", err)
			}
		}
	}

	// Audit log — member banned
	actorID := resolveActorFromRequest(r)
	logAudit(u.ALDB, cID, "member.banned", "member", actorID, resolveActorName(u.DB, actorID), userID, "", nil)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User banned from community successfully"}`))
}

// UpdateUserByIDHandler updates a user by ID
func (u User) UpdateUserByIDHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Parse the request body to get the updated user details
	var updatedFields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedFields); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Set the updatedAt field to the current time
	updatedFields["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Create an update document targeting the internal user object
	update := bson.M{}
	for key, value := range updatedFields {
		update["user."+key] = value
	}

	// Update the user in the database
	filter := bson.M{"_id": uID}
	_, err = u.DB.UpdateOne(ctx, filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update user", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User updated successfully"}`))
}

// BlockUserHandler blocks a user by updating or inserting the friendId with status "blocked"
func (u User) BlockUserHandler(w http.ResponseWriter, r *http.Request) {
	// Parse the request body to get the userId and friendId
	var requestBody struct {
		UserID   string `json:"userId"`
		FriendID string `json:"friendId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the userId and friendId to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	fID, err := primitive.ObjectIDFromHex(requestBody.FriendID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Retrieve the user's friends array
	user := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's friends", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the friends array is nil or empty
	if user.Details.Friends == nil || len(user.Details.Friends) == 0 {
		newFriend := bson.M{
			"friend_id":  fID,
			"status":     "blocked",
			"created_at": time.Now(),
		}
		update := bson.M{"$set": bson.M{"user.friends": []bson.M{newFriend}}}
		_, err = u.DB.UpdateOne(ctx, bson.M{"_id": uID}, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's friends", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Check if the friendId exists in the user's friends list
		filter := bson.M{"_id": uID, "user.friends.friend_id": requestBody.FriendID}
		update := bson.M{"$set": bson.M{"user.friends.$.status": "blocked"}}
		result, err := u.DB.UpdateOne(ctx, filter, update)
		if err != nil {
			config.ErrorStatus("failed to update friend status", http.StatusInternalServerError, w, err)
			return
		}

		// If the friendId does not exist, insert a new object with status "blocked"
		if result.MatchedCount == 0 {
			newFriend := bson.M{
				"friend_id":  fID,
				"status":     "blocked",
				"created_at": time.Now(),
			}
			update = bson.M{"$push": bson.M{"user.friends": newFriend}}
			_, err = u.DB.UpdateOne(ctx, bson.M{"_id": uID}, update)
			if err != nil {
				config.ErrorStatus("failed to insert new friend with status blocked", http.StatusInternalServerError, w, err)
				return
			}
		}
	}

	// Remove the userId from the friendId's friends list
	friendFilter := bson.M{"_id": fID}
	friendUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.UserID}}}
	_, err = u.DB.UpdateOne(ctx, friendFilter, friendUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove user from friend's friends list", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User blocked successfully"}`))
}

// UnblockUserHandler unblocks a user by removing the friendId from the user's friends list
func (u User) UnblockUserHandler(w http.ResponseWriter, r *http.Request) {
	// Parse the request body to get the userId and friendId
	var requestBody struct {
		UserID   string `json:"userId"`
		FriendID string `json:"friendId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the userId to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Remove the friendId from the user's friends list
	filter := bson.M{"_id": uID}
	update := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.FriendID}}}
	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to remove friend from user's friends list", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User unblocked successfully"}`))
}

// SetOnlineStatusHandler updates the online status of a user
func (u User) SetOnlineStatusHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID   string `json:"userId"`
		IsOnline bool   `json:"isOnline"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": uID}
	update := bson.M{"$set": bson.M{"user.isOnline": requestBody.IsOnline}}

	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		config.ErrorStatus("failed to update online status", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User online status updated successfully"}`))
}

// UnfriendUserHandler unfriends a user by removing the friendId from the user's friends list
func (u User) UnfriendUserHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID   string `json:"userId"`
		FriendID string `json:"friendId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	fID, err := primitive.ObjectIDFromHex(requestBody.FriendID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Remove the friend from the user's friends list
	userFilter := bson.M{"_id": uID}
	userUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.FriendID}}}
	_, err = u.DB.UpdateOne(ctx, userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove friend from user's friends list", http.StatusInternalServerError, w, err)
		return
	}

	// Remove the user from the friend's friends list
	friendFilter := bson.M{"_id": fID}
	friendUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.UserID}}}
	_, err = u.DB.UpdateOne(ctx, friendFilter, friendUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove user from friend's friends list", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User unfriended successfully"}`))
}

// AddUserToPendingDepartmentHandler adds a user to a department's members list with status "pending"
func (u User) AddUserToPendingDepartmentHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID := vars["userId"]

	var requestBody struct {
		CommunityID  string `json:"communityId"`
		DepartmentID string `json:"departmentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert IDs to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(requestBody.DepartmentID)
	if err != nil {
		config.ErrorStatus("invalid departmentId", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the community by ID
	community, err := u.CDB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to find community by ID", http.StatusNotFound, w, err)
		return
	}

	// Find the department within the community
	var department *models.Department
	for i, dept := range community.Details.Departments {
		if dept.ID == dID {
			department = &community.Details.Departments[i]
			break
		}
	}
	if department == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, nil)
		return
	}

	// Initialize members if it is nil
	if department.Members == nil {
		department.Members = []models.MemberStatus{}
	}

	// Check if user already exists in the members list
	for _, existingMember := range department.Members {
		if existingMember.UserID == userID {
			// User already has a request or is a member
			if existingMember.Status == "pending" {
				config.ErrorStatus("You have already requested to join this department", http.StatusBadRequest, w, nil)
				return
			} else if existingMember.Status == "approved" {
				config.ErrorStatus("You are already a member of this department", http.StatusBadRequest, w, nil)
				return
			}
		}
	}

	// Add the user to the department's members list with status "pending"
	member := models.MemberStatus{
		UserID: userID,
		Status: "pending",
	}
	department.Members = append(department.Members, member)

	// Update the community in the database
	update := bson.M{"$set": bson.M{"community.departments": community.Details.Departments}}
	err = u.CDB.UpdateOne(ctx, bson.M{"_id": cID}, update)
	if err != nil {
		config.ErrorStatus("failed to update community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User added to department with pending status successfully"}`))
}

// CancelPendingDepartmentRequestHandler cancels a pending department join request for a user
func (u User) CancelPendingDepartmentRequestHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	var requestBody struct {
		CommunityID  string `json:"communityId"`
		DepartmentID string `json:"departmentId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(requestBody.DepartmentID)
	if err != nil {
		config.ErrorStatus("invalid departmentId", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find the community
	community, err := u.CDB.FindOne(ctx, bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to find community", http.StatusNotFound, w, err)
		return
	}

	// Find the department and verify user has a pending request
	var deptFound bool
	var memberFound bool
	for i, dept := range community.Details.Departments {
		if dept.ID == dID {
			deptFound = true
			newMembers := make([]models.MemberStatus, 0, len(dept.Members))
			for _, m := range dept.Members {
				if m.UserID == userID && m.Status == "pending" {
					memberFound = true
					continue // skip this entry to remove it
				}
				newMembers = append(newMembers, m)
			}
			community.Details.Departments[i].Members = newMembers
			break
		}
	}

	if !deptFound {
		config.ErrorStatus("department not found", http.StatusNotFound, w, nil)
		return
	}
	if !memberFound {
		config.ErrorStatus("no pending department request found", http.StatusBadRequest, w, fmt.Errorf("no pending department request found"))
		return
	}

	// Update the community with the modified departments array
	update := bson.M{"$set": bson.M{"community.departments": community.Details.Departments}}
	err = u.CDB.UpdateOne(ctx, bson.M{"_id": cID}, update)
	if err != nil {
		config.ErrorStatus("failed to cancel pending department request", http.StatusInternalServerError, w, err)
		return
	}

	// Clean up join_request notifications sent to admins for this department
	// Department notifications use type "join_request" with data1=communityId, data3=departmentId
	notifFilter := bson.M{
		"user.notifications": bson.M{
			"$elemMatch": bson.M{
				"type":       "join_request",
				"sentFromID": userID,
				"data1":      requestBody.CommunityID,
				"data3":      requestBody.DepartmentID,
			},
		},
	}
	notifUpdate := bson.M{
		"$pull": bson.M{
			"user.notifications": bson.M{
				"type":       "join_request",
				"sentFromID": userID,
				"data1":      requestBody.CommunityID,
				"data3":      requestBody.DepartmentID,
			},
		},
	}
	// Best-effort cleanup
	_, _ = u.DB.UpdateMany(ctx, notifFilter, notifUpdate)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Pending department request cancelled successfully"}`))
}

// CreateCheckoutSessionHandler subscribes a user to a specific tier
func (u User) CreateCheckoutSessionHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID   string `json:"userId"`
		Tier     string `json:"tier"`
		IsAnnual bool   `json:"isAnnual"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if requestBody.UserID == "" {
		config.ErrorStatus("user ID is required", http.StatusBadRequest, w, nil)
		return
	}

	if requestBody.Tier == "" {
		config.ErrorStatus("tier is required", http.StatusBadRequest, w, nil)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Check for existing app store subscription before creating checkout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": userID}
	var user models.User
	err = u.DB.FindOne(ctx, filter).Decode(&user)
	if err != nil && err != mongo.ErrNoDocuments {
		config.ErrorStatus("failed to find user", http.StatusInternalServerError, w, err)
		return
	}

	// Block if user has active app store subscription
	if user.Details.Subscription.Active {
		source := user.Details.Subscription.Source
		if source == "app_store" || source == "revenuecat" {
			config.ErrorStatus("You have an active subscription through the App Store. Please manage it in your app or device settings.", http.StatusBadRequest, w, nil)
			return
		}
	}

	bInterval := "monthly"
	if requestBody.IsAnnual {
		bInterval = "annual"
	}

	cSession := &CheckoutRequest{
		UserID:          requestBody.UserID,
		Tier:            requestBody.Tier,
		BillingInterval: bInterval,
	}

	checkoutSession, err := createCheckoutSession(cSession)

	if err != nil {
		config.ErrorStatus("failed to create checkout session", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"checkoutSession": checkoutSession,
	})
}

// CheckoutRequest represents the request body for creating a checkout session
type CheckoutRequest struct {
	UserID          string `json:"userId"`
	Tier            string `json:"tier"`
	BillingInterval string `json:"billingInterval"`
}

// userTierPriceID returns the V2 (post-price-drop) Stripe price ID for a user
// tier if it is set, otherwise falls back to the V1 (original) price ID. The
// fallback lets us deploy this code before V2 prices exist in Stripe.
func userTierPriceID(v2Env, v1Env string) string {
	if id := os.Getenv(v2Env); id != "" {
		return id
	}
	return os.Getenv(v1Env)
}

func createCheckoutSession(c *CheckoutRequest) (*stripe.CheckoutSession, error) {
	var priceID string
	tier := strings.ToLower(c.Tier)
	billingInterval := strings.ToLower(c.BillingInterval)

	if billingInterval != "monthly" && billingInterval != "annual" {
		// http.Error(w, "Invalid billingInterval. Must be one of: monthly, annual", http.StatusBadRequest)
		return nil, fmt.Errorf("invalid billingInterval. Must be one of: monthly, annual")
	}

	switch tier {
	case "base":
		if billingInterval == "monthly" {
			priceID = userTierPriceID("STRIPE_BASE_V2_MONTHLY_PRICE_ID", "STRIPE_BASE_MONTHLY_PRICE_ID")
		} else {
			priceID = userTierPriceID("STRIPE_BASE_V2_ANNUAL_PRICE_ID", "STRIPE_BASE_ANNUAL_PRICE_ID")
		}
	case "premium":
		if billingInterval == "monthly" {
			priceID = userTierPriceID("STRIPE_PREMIUM_V2_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_MONTHLY_PRICE_ID")
		} else {
			priceID = userTierPriceID("STRIPE_PREMIUM_V2_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_ANNUAL_PRICE_ID")
		}
	case "premium_plus":
		if billingInterval == "monthly" {
			priceID = userTierPriceID("STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID")
		} else {
			priceID = userTierPriceID("STRIPE_PREMIUM_PLUS_V2_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID")
		}
	case "promotion_basic":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_BASIC_PROMOTION_MONTHLY_PRICE_ID")
		}
	case "promotion_standard":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_STANDARD_PROMOTION_MONTHLY_PRICE_ID")
		}
	case "promotion_premium":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_PREMIUM_PROMOTION_MONTHLY_PRICE_ID")
		}
	case "promotion_elite":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_ELITE_PROMOTION_MONTHLY_PRICE_ID")
		}
	default:
		// http.Error(w, "Invalid tier. Must be one of: base, premium, premium_plus", http.StatusBadRequest)
		return nil, fmt.Errorf("invalid tier. Must be one of: base, premium, premium_plus")
	}

	if priceID == "" {
		return nil, fmt.Errorf("price ID for tier %s and billing interval %s is not set", c.Tier, c.BillingInterval)
	}

	// Use frontend URLs for web subscriptions
	frontendURL := os.Getenv("PUBLIC_WEB_BASE_URL")
	if frontendURL == "" {
		frontendURL = "https://www.linespolice-cad.com"
	}
	successURL := fmt.Sprintf("%s/subscription/success?session_id={CHECKOUT_SESSION_ID}", frontendURL)
	cancelURL := fmt.Sprintf("%s/subscription/cancel", frontendURL)

	// Create a Stripe Checkout Session
	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Mode:               stripe.String("subscription"),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceID),
				Quantity: stripe.Int64(1),
			},
		},
		Metadata: map[string]string{
			"userId":          c.UserID,
			"tier":            tier,
			"billingInterval": billingInterval,
			"source":          "stripe",
		},
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
	}

	return session.New(params)

}

// VerifyRequest represents the request body for verifying a subscription
type VerifyRequest struct {
	SessionID string `json:"sessionId"`
}

// VerifyResponse represents the response body for verifying a subscription
type VerifyResponse struct {
	Success      bool `json:"success"`
	Subscription struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		Plan            string `json:"plan"`            // e.g., "base", "premium", "premium_plus"
		BillingInterval string `json:"billingInterval"` // e.g., "monthly", "annual"
		UserID          string `json:"userId"`
	} `json:"subscription"`
	Error string `json:"error,omitempty"`
}

// VerifySubscriptionHandler verifies a subscription by checking the payment status
func (u User) VerifySubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if req.SessionID == "" {
		config.ErrorStatus("sessionId is required", http.StatusBadRequest, w, nil)
		return
	}

	// Retrieve the Checkout Session
	checkoutSession, err := session.Get(req.SessionID, nil)
	if err != nil {
		config.ErrorStatus("failed to retrieve checkout session", http.StatusInternalServerError, w, err)
		return
	}

	// Prepare the response
	resp := VerifyResponse{}

	if checkoutSession.PaymentStatus == "paid" {
		// Fetch subscription details
		subs, err := subscription.Get(checkoutSession.Subscription.ID, nil)
		if err != nil {
			config.ErrorStatus("failed to retrieve subscription", http.StatusInternalServerError, w, err)
			return
		}

		// Map the Price ID back to the tier and billing interval
		plan := "unknown"
		billingInterval := "unknown"
		switch subs.Items.Data[0].Price.ID {
		case os.Getenv("STRIPE_BASE_MONTHLY_PRICE_ID"):
			plan = "base"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_BASE_ANNUAL_PRICE_ID"):
			plan = "base"
			billingInterval = "annual"
		case os.Getenv("STRIPE_PREMIUM_MONTHLY_PRICE_ID"):
			plan = "premium"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_PREMIUM_ANNUAL_PRICE_ID"):
			plan = "premium"
			billingInterval = "annual"
		case os.Getenv("STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID"):
			plan = "premium_plus"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID"):
			plan = "premium_plus"
			billingInterval = "annual"
		case os.Getenv("STRIPE_BASIC_PROMOTION_MONTHLY_PRICE_ID"):
			plan = "basic"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_STANDARD_PROMOTION_MONTHLY_PRICE_ID"):
			plan = "standard"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_PREMIUM_PROMOTION_MONTHLY_PRICE_ID"):
			plan = "premium"
			billingInterval = "monthly"
		case os.Getenv("STRIPE_ELITE_PROMOTION_MONTHLY_PRICE_ID"):
			plan = "elite"
			billingInterval = "monthly"
		}

		resp.Success = true
		resp.Subscription.ID = subs.ID
		resp.Subscription.Status = string(subs.Status)
		resp.Subscription.Plan = plan
		resp.Subscription.BillingInterval = billingInterval
		resp.Subscription.UserID = checkoutSession.Metadata["userId"] // For User subscriptions this will be the userID, for Community subscriptions this will be the communityID
	} else {
		resp.Success = false
		resp.Error = "Payment not completed"
	}

	// Respond with the verification result
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// SubscriptionTier represents a subscription tier for the pricing page
type SubscriptionTier struct {
	Name                 string   `json:"name"`
	Key                  string   `json:"key"`
	MonthlyPrice         float64  `json:"monthlyPrice"`
	AnnualPrice          float64  `json:"annualPrice"`
	OriginalMonthlyPrice float64  `json:"originalMonthlyPrice,omitempty"`
	OriginalAnnualPrice  float64  `json:"originalAnnualPrice,omitempty"`
	OnSale               bool     `json:"onSale,omitempty"`
	Features             []string `json:"features"`
	Color                string   `json:"color"`
	Popular              bool     `json:"popular,omitempty"`
}

// CommunityTier represents a community promotional tier
type CommunityTier struct {
	Name         string   `json:"name"`
	Key          string   `json:"key"`
	MonthlyPrice float64  `json:"monthlyPrice"`
	Features     []string `json:"features"`
	Color        string   `json:"color"`
	Popular      bool     `json:"popular,omitempty"`
}

// GetSubscriptionTiersHandler returns the available subscription tiers (public endpoint).
// When KICKBACK_PRICE_DROP_LIVE=true the response uses the post-drop V2 prices
// and sets OnSale=true with the old prices in OriginalMonthlyPrice / OriginalAnnualPrice
// so clients render the SALE badge + strikethrough. Default off — JSON output
// is byte-identical to today.
func (u User) GetSubscriptionTiersHandler(w http.ResponseWriter, r *http.Request) {
	priceDropLive, _ := strconv.ParseBool(os.Getenv("KICKBACK_PRICE_DROP_LIVE"))

	base := SubscriptionTier{
		Name: "Base", Key: "base",
		MonthlyPrice: 3, AnnualPrice: 32,
		Features: []string{"Create up to 5 communities", "Default departments", "Full ads"},
		Color:    "#3b82f6",
	}
	premium := SubscriptionTier{
		Name: "Premium", Key: "premium",
		MonthlyPrice: 8, AnnualPrice: 85,
		Features: []string{"Create up to 10 communities", "Verified badge", "50% fewer ads"},
		Color:    "#667eea",
		Popular:  true,
	}
	premiumPlus := SubscriptionTier{
		Name: "Premium Plus", Key: "premium_plus",
		MonthlyPrice: 19.99, AnnualPrice: 209,
		Features: []string{"Create unlimited communities", "No ads", "Verified badge"},
		Color:    "#fbbf24",
	}

	if priceDropLive {
		base.OriginalMonthlyPrice, base.OriginalAnnualPrice = base.MonthlyPrice, base.AnnualPrice
		base.MonthlyPrice, base.AnnualPrice = 2, 20
		base.OnSale = true

		premium.OriginalMonthlyPrice, premium.OriginalAnnualPrice = premium.MonthlyPrice, premium.AnnualPrice
		premium.MonthlyPrice, premium.AnnualPrice = 5, 50
		premium.OnSale = true

		premiumPlus.OriginalMonthlyPrice, premiumPlus.OriginalAnnualPrice = premiumPlus.MonthlyPrice, premiumPlus.AnnualPrice
		premiumPlus.MonthlyPrice, premiumPlus.AnnualPrice = 9.99, 99
		premiumPlus.OnSale = true
	}

	tiers := []SubscriptionTier{
		{
			Name:         "Free",
			Key:          "free",
			MonthlyPrice: 0,
			AnnualPrice:  0,
			Features:     []string{"Create 1 community", "Default departments", "Full ads"},
			Color:        "#718096",
		},
		base,
		premium,
		premiumPlus,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tiers": tiers,
	})
}

// GetCommunityTiersHandler returns the available community promotional tiers (public endpoint)
func (u User) GetCommunityTiersHandler(w http.ResponseWriter, r *http.Request) {
	tiers := []CommunityTier{
		{
			Name:         "Basic",
			Key:          "basic",
			MonthlyPrice: 5,
			Features:     []string{"Promotional text in search", "Discord community promotion"},
			Color:        "#3b82f6",
		},
		{
			Name:         "Standard",
			Key:          "standard",
			MonthlyPrice: 10,
			Features:     []string{"Promotional text in search", "Discord promotion with image gallery", "Verified community badge", "Short description (100 chars)"},
			Color:        "#10b981",
		},
		{
			Name:         "Premium",
			Key:          "premium",
			MonthlyPrice: 20,
			Features:     []string{"Promotional text in search", "Discord promotion with banner image", "Verified community badge", "Boost on Discover page"},
			Color:        "#667eea",
		},
		{
			Name:         "Elite",
			Key:          "elite",
			MonthlyPrice: 50,
			Features:     []string{"Promotional text in search", "Featured Discord promotion", "Verified community badge", "Boost on Discover page", "Featured on Home Page", "Long description (200 chars)"},
			Color:        "#fbbf24",
			Popular:      true,
		},
	}

	durations := []int{1, 3, 6}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tiers":     tiers,
		"durations": durations,
	})
}

// CheckSubscriptionSourceResponse represents the response for checking subscription source
type CheckSubscriptionSourceResponse struct {
	HasActiveSubscription bool   `json:"hasActiveSubscription"`
	Source                string `json:"source"`
	Plan                  string `json:"plan"`
	CanPurchaseWeb        bool   `json:"canPurchaseWeb"`
	Message               string `json:"message,omitempty"`
}

// CheckSubscriptionSourceHandler checks if a user has an app store subscription
func (u User) CheckSubscriptionSourceHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID string `json:"userId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if requestBody.UserID == "" {
		config.ErrorStatus("user ID is required", http.StatusBadRequest, w, nil)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": userID}
	var user models.User
	err = u.DB.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to find user", http.StatusNotFound, w, err)
		return
	}

	resp := CheckSubscriptionSourceResponse{
		HasActiveSubscription: false,
		Source:                "",
		Plan:                  "",
		CanPurchaseWeb:        true,
		Message:               "",
	}

	if user.Details.Subscription.Active {
		resp.HasActiveSubscription = true
		resp.Source = user.Details.Subscription.Source
		resp.Plan = user.Details.Subscription.Plan

		// Block web purchase if subscription is from app store
		if resp.Source == "app_store" || resp.Source == "revenuecat" {
			resp.CanPurchaseWeb = false
			resp.Message = "You have an active subscription through the App Store. Please manage it in your app or device settings."
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CreatePortalSessionHandler creates a Stripe customer portal session for subscription management
func (u User) CreatePortalSessionHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID string `json:"userId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if requestBody.UserID == "" {
		config.ErrorStatus("user ID is required", http.StatusBadRequest, w, nil)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	filter := bson.M{"_id": userID}
	var user models.User
	err = u.DB.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to find user", http.StatusNotFound, w, err)
		return
	}

	// Check if user has a Stripe customer ID
	if user.Details.Subscription.StripeCustomerID == "" {
		config.ErrorStatus("No Stripe subscription found. Please subscribe via the web first.", http.StatusBadRequest, w, nil)
		return
	}

	// Check if subscription is from Stripe
	if user.Details.Subscription.Source != "stripe" && user.Details.Subscription.Source != "" {
		config.ErrorStatus("Your subscription is managed through the App Store. Please manage it there.", http.StatusBadRequest, w, nil)
		return
	}

	// Create a Stripe customer portal session
	returnURL := os.Getenv("PUBLIC_WEB_BASE_URL")
	if returnURL == "" {
		returnURL = "https://www.linespolice-cad.com"
	}
	returnURL = returnURL + "/manage-subscription"

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(user.Details.Subscription.StripeCustomerID),
		ReturnURL: stripe.String(returnURL),
	}

	portalSession, err := portalsession.New(params)
	if err != nil {
		config.ErrorStatus("failed to create portal session", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url": portalSession.URL,
	})
}

// SubscribeUserHandler subscribes a user to a specific tier. Called by the
// mobile app after a successful RevenueCat purchase. Accepts the legacy
// body (userId, subscriptionId, status, tier, isAnnual) plus two optional
// fields newer mobile builds send: store ("play_store" | "app_store") and
// productId. Both are optional so older app builds in the wild keep
// working unchanged.
func (u User) SubscribeUserHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID         string `json:"userId"`
		SubscriptionID string `json:"subscriptionId"`
		Status         string `json:"status"`
		Tier           string `json:"tier"`
		IsAnnual       bool   `json:"isAnnual"`
		Store          string `json:"store,omitempty"`     // optional: "play_store" | "app_store"
		ProductID      string `json:"productId,omitempty"` // optional: RevenueCat / store product id
	}

	// Read the raw body once so we can both decode it and persist it to
	// the audit trail.
	rawBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		config.ErrorStatus("failed to read request body", http.StatusBadRequest, w, err)
		return
	}
	if err := json.Unmarshal(rawBody, &requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	isActive := requestBody.Status == "active"

	// Load the existing subscription so we can decide whether this is a
	// first-time subscribe (set createdAt) or a re-subscribe (keep the
	// original createdAt as the truth of when they first paid).
	var existing models.User
	hasExisting := false
	if err := u.DB.FindOne(ctx, bson.M{"_id": userID}).Decode(&existing); err == nil {
		hasExisting = existing.Details.Subscription.CreatedAt != nil &&
			existing.Details.Subscription.CreatedAt != ""
	}

	set := bson.M{
		"user.subscription.id":        requestBody.SubscriptionID,
		"user.subscription.plan":      requestBody.Tier,
		"user.subscription.isAnnual":  requestBody.IsAnnual,
		"user.subscription.active":    isActive,
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}
	if !hasExisting {
		set["user.subscription.createdAt"] = primitive.NewDateTimeFromTime(time.Now())
	}
	if source := mapStoreToSource(requestBody.Store); source != "" {
		set["user.subscription.source"] = source
	}

	if _, err := u.DB.UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$set": set}); err != nil {
		config.ErrorStatus("failed to subscribe user", http.StatusInternalServerError, w, err)
		return
	}

	plan := requestBody.Tier
	isAnnual := requestBody.IsAnnual
	if p, a, ok := parseProductID(requestBody.ProductID); ok {
		plan = p
		isAnnual = a
	}
	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}
	rec.record(ctx, subscriptionEventInput{
		Provider:      "mobile_app",
		EventType:     "mobile_subscribe",
		Store:         strings.ToUpper(requestBody.Store),
		UserIDHint:    requestBody.UserID,
		Plan:          plan,
		IsAnnual:      isAnnual,
		ProductID:     requestBody.ProductID,
		TransactionID: requestBody.SubscriptionID,
		RawPayload:    rawBody,
		SourceIP:      r.RemoteAddr,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User subscribed successfully",
	})
}

// HandleStripeWebhook handles Stripe webhook events
// Note: Delivery Delays
// Most webhooks are usually delivered within 5 to 60 seconds of the event occurring -
// **cancellation events usually are delivered within 2hrs** of the user cancelling their subscription.
// You should be aware of these delivery times when designing your app.
func (u User) HandleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		config.ErrorStatus("failed to read webhook payload", http.StatusBadRequest, w, err)
		return
	}

	// Get the webhook secret from environment
	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	if webhookSecret == "" {
		config.ErrorStatus("webhook secret not configured", http.StatusInternalServerError, w, nil)
		return
	}

	// Get the Stripe signature from headers
	signature := r.Header.Get("Stripe-Signature")
	if signature == "" {
		config.ErrorStatus("missing stripe signature", http.StatusBadRequest, w, nil)
		return
	}

	// Verify the webhook signature
	event, err := verifyWebhookSignature(payload, signature, webhookSecret)
	if err != nil {
		config.ErrorStatus("invalid webhook signature", http.StatusBadRequest, w, err)
		return
	}

	zap.S().Infow("Received Stripe webhook event", "eventType", event.Type, "eventId", event.ID)

	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}

	// Stripe retries on non-2xx and on socket failures. Pre-flight dedupe
	// avoids re-running handlers; the unique (provider, providerEventId)
	// index catches concurrent-delivery races.
	if event.ID != "" && rec.isDuplicate(r.Context(), "stripe", event.ID) {
		zap.S().Infow("duplicate Stripe event — skipping handler",
			"eventId", event.ID, "eventType", string(event.Type))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "duplicate event — already processed"}`))
		return
	}

	// Best-effort extraction of the subscription/customer ids from the raw
	// event so we can attach the audit row to a user. We don't need this
	// for correctness (the existing handlers do their own lookup), only
	// for searchability of the subscription_events row.
	stripeSubID, stripeCustomerID := extractStripeIdentifiers(event.Data.Raw)

	rr := rec.record(r.Context(), subscriptionEventInput{
		Provider:          "stripe",
		ProviderEventID:   event.ID,
		EventType:         string(event.Type),
		Store:             "STRIPE",
		TransactionIDHint: stripeSubID,
		TransactionID:     stripeSubID,
		ProductID:         stripeCustomerID, // not a product, but useful for searchability via the lookup endpoint
		Environment:       stripeEnv(event.Livemode),
		RawPayload:        payload,
		SourceIP:          r.RemoteAddr,
	})
	if rr.Duplicate {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "duplicate event — already processed"}`))
		return
	}

	// Handle different event types
	switch event.Type {
	case "checkout.session.completed":
		err = u.handleCheckoutSessionCompleted(*event)
	case "invoice.payment_succeeded":
		err = u.handleInvoicePaymentSucceeded(*event)
	case "invoice.payment_failed":
		err = u.handleInvoicePaymentFailed(*event)
	case "customer.subscription.updated":
		err = u.handleSubscriptionUpdated(*event)
	case "customer.subscription.deleted":
		err = u.handleSubscriptionDeleted(*event)
	case "customer.subscription.trial_will_end":
		err = u.handleSubscriptionTrialWillEnd(*event)
	default:
		zap.S().Infof("Unhandled event type: %s", event.Type)
	}

	if err != nil {
		zap.S().Errorf("Error handling webhook event %s: %v", event.Type, err)
		config.ErrorStatus("failed to process webhook", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Webhook processed successfully"}`))
}

// verifyWebhookSignature verifies the Stripe webhook signature
func verifyWebhookSignature(payload []byte, signature string, secret string) (*stripe.Event, error) {
	// Parse the signature header
	// Format: t=timestamp,v1=signature,v0=signature (newer format)
	// or: t=timestamp,v1=signature (older format)
	parts := strings.Split(signature, ",")
	
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("invalid signature format")
	}

	// Extract timestamp and signature
	var timestamp, sig string
	for _, part := range parts {
		if strings.HasPrefix(part, "t=") {
			timestamp = strings.TrimPrefix(part, "t=")
		} else if strings.HasPrefix(part, "v1=") {
			sig = strings.TrimPrefix(part, "v1=")
		}
		// Note: We ignore v0 signatures as they're for older webhook versions
	}

	if timestamp == "" || sig == "" {
		return nil, fmt.Errorf("missing timestamp or signature")
	}

	// Create the signed payload
	signedPayload := timestamp + "." + string(payload)

	// Create HMAC-SHA256 hash
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(signedPayload))
	expectedSignature := hex.EncodeToString(h.Sum(nil))

	// Compare signatures
	if !hmac.Equal([]byte(sig), []byte(expectedSignature)) {
		return nil, fmt.Errorf("signature verification failed")
	}

	// Parse the event
	var event stripe.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("failed to parse event: %v", err)
	}

	return &event, nil
}

// min helper function
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// subscriptionPeriodFields returns the current billing period start/end as a
// bson fragment ready to merge into a $set. In Stripe API v82+ the current
// period window lives on the subscription item, not the top-level subscription,
// so callers must read it from sub.Items.Data[0].
func subscriptionPeriodFields(sub *stripe.Subscription) bson.M {
	fields := bson.M{}
	if sub == nil || sub.Items == nil || len(sub.Items.Data) == 0 {
		return fields
	}
	item := sub.Items.Data[0]
	if item.CurrentPeriodStart > 0 {
		fields["user.subscription.currentPeriodStart"] = primitive.NewDateTimeFromTime(time.Unix(item.CurrentPeriodStart, 0).UTC())
	}
	if item.CurrentPeriodEnd > 0 {
		fields["user.subscription.currentPeriodEnd"] = primitive.NewDateTimeFromTime(time.Unix(item.CurrentPeriodEnd, 0).UTC())
	}
	return fields
}

// handleCheckoutSessionCompleted handles successful checkout sessions
func (u User) handleCheckoutSessionCompleted(event stripe.Event) error {
	var checkoutSession stripe.CheckoutSession
	err := json.Unmarshal(event.Data.Raw, &checkoutSession)
	if err != nil {
		return fmt.Errorf("failed to parse checkout session: %v", err)
	}

	// Check if this is a community promotion (one-time payment)
	if checkoutSession.Metadata["type"] == "community_promotion" {
		return u.handleCommunityPromotionCompleted(checkoutSession)
	}

	// Extract metadata
	userID := checkoutSession.Metadata["userId"]
	billingInterval := checkoutSession.Metadata["billingInterval"]

	// For test events or sessions without metadata, log and skip processing
	if userID == "" {
		zap.S().Infof("Skipping checkout session %s: missing userId in metadata (likely a test event)", checkoutSession.ID)
		return nil
	}

	// Get subscription details
	sub, err := subscription.Get(checkoutSession.Subscription.ID, nil)
	if err != nil {
		return fmt.Errorf("failed to get subscription: %v", err)
	}

	// Map the Price ID back to the tier and billing interval. Uses the
	// shared V1+V2-aware mapper so post-price-drop (V2) price ids resolve
	// correctly — an inline V1-only switch here previously wrote
	// plan="unknown" for migrated/new-price subscriptions.
	plan, isAnnual := mapStripePriceIDToPlan(sub.Items.Data[0].Price.ID)
	if plan == "unknown" {
		zap.S().Warnf("handleCheckoutSessionCompleted: unmapped Stripe price id %q for user %s (sub %s) — storing plan=unknown",
			sub.Items.Data[0].Price.ID, userID, sub.ID)
	}

	// Update user subscription in database
	userObjID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return fmt.Errorf("invalid user ID: %v", err)
	}

	// Get customer ID from the checkout session
	customerID := ""
	if checkoutSession.Customer != nil {
		customerID = checkoutSession.Customer.ID
	}

	filter := bson.M{"_id": userObjID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.id":               sub.ID,
			"user.subscription.plan":             plan,
			"user.subscription.isAnnual":         isAnnual,
			"user.subscription.active":           sub.Status == "active",
			"user.subscription.source":           "stripe",
			"user.subscription.stripeCustomerId": customerID,
			"user.subscription.createdAt":        primitive.NewDateTimeFromTime(time.Now()),
			"user.subscription.updatedAt":        primitive.NewDateTimeFromTime(time.Now()),
		},
	}
	for k, v := range subscriptionPeriodFields(sub) {
		update["$set"].(bson.M)[k] = v
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}

	zap.S().Infof("Successfully updated subscription for user %s: %s (%s) via stripe", userID, plan, billingInterval)
	return nil
}

// handleCommunityPromotionCompleted handles successful community promotion payments
func (u User) handleCommunityPromotionCompleted(checkoutSession stripe.CheckoutSession) error {
	// Extract metadata
	userID := checkoutSession.Metadata["userId"]
	communityID := checkoutSession.Metadata["communityId"]
	tier := checkoutSession.Metadata["tier"]
	durationMonthsStr := checkoutSession.Metadata["durationMonths"]
	expirationDateStr := checkoutSession.Metadata["expirationDate"]

	if communityID == "" {
		return fmt.Errorf("missing communityId in metadata")
	}

	durationMonths, _ := strconv.Atoi(durationMonthsStr)
	if durationMonths == 0 {
		durationMonths = 1
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		return fmt.Errorf("invalid community ID: %v", err)
	}

	purchaseDate := time.Now().Format(time.RFC3339)

	// Determine expiration date based on whether this is a same-tier extension
	var expirationDate string
	community, fetchErr := u.CDB.FindOne(context.Background(), bson.M{"_id": cID})
	if fetchErr == nil && community.Details.Subscription.Active &&
		strings.EqualFold(community.Details.Subscription.Plan, tier) {
		// Same tier: extend from current expiration date (or now if already expired)
		baseTime := time.Now()
		if community.Details.Subscription.ExpirationDate != "" {
			if parsed, parseErr := time.Parse(time.RFC3339, community.Details.Subscription.ExpirationDate); parseErr == nil && parsed.After(baseTime) {
				baseTime = parsed
			}
		}
		expirationDate = baseTime.AddDate(0, durationMonths, 0).Format(time.RFC3339)
		zap.S().Infof("Extending same-tier boost (%s) for community %s: new expiration %s", tier, communityID, expirationDate)
	} else {
		// New or upgrade: use metadata expiration or calculate from now
		expirationDate = expirationDateStr
		if expirationDate == "" {
			expirationDate = time.Now().AddDate(0, durationMonths, 0).Format(time.RFC3339)
		}
	}

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.subscriptionCreatedBy": userID,
			"community.subscription.id":             checkoutSession.PaymentIntent.ID,
			"community.subscription.plan":           tier,
			"community.subscription.active":         true,
			"community.subscription.source":         "stripe",
			"community.subscription.purchaseDate":   purchaseDate,
			"community.subscription.expirationDate": expirationDate,
			"community.subscription.durationMonths": durationMonths,
			"community.subscription.createdAt":      primitive.NewDateTimeFromTime(time.Now()),
			"community.subscription.updatedAt":      primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = u.CDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update community promotion: %v", err)
	}

	zap.S().Infof("Successfully updated community promotion for community %s: %s (%d months) via stripe", communityID, tier, durationMonths)
	return nil
}

// handleInvoicePaymentSucceeded handles successful invoice payments
func (u User) handleInvoicePaymentSucceeded(event stripe.Event) error {
	var invoice stripe.Invoice
	err := json.Unmarshal(event.Data.Raw, &invoice)
	if err != nil {
		return fmt.Errorf("failed to parse invoice: %v", err)
	}

	// Only handle subscription invoices
	var subscriptionID string
	if invoice.Parent != nil && invoice.Parent.SubscriptionDetails != nil && invoice.Parent.SubscriptionDetails.Subscription != nil {
		subscriptionID = invoice.Parent.SubscriptionDetails.Subscription.ID
	}
	if subscriptionID == "" {
		return nil
	}

	// Get subscription details
	sub, err := subscription.Get(subscriptionID, nil)
	if err != nil {
		return fmt.Errorf("failed to get subscription: %v", err)
	}

	// Find user by subscription ID
	filter := bson.M{"user.subscription.id": sub.ID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.active":    sub.Status == "active",
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}
	for k, v := range subscriptionPeriodFields(sub) {
		update["$set"].(bson.M)[k] = v
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}

	zap.S().Infof("Successfully processed payment for subscription %s", sub.ID)
	return nil
}

// handleInvoicePaymentFailed handles failed invoice payments
func (u User) handleInvoicePaymentFailed(event stripe.Event) error {
	var invoice stripe.Invoice
	err := json.Unmarshal(event.Data.Raw, &invoice)
	if err != nil {
		return fmt.Errorf("failed to parse invoice: %v", err)
	}

	// Only handle subscription invoices
	var subscriptionID string
	if invoice.Parent != nil && invoice.Parent.SubscriptionDetails != nil && invoice.Parent.SubscriptionDetails.Subscription != nil {
		subscriptionID = invoice.Parent.SubscriptionDetails.Subscription.ID
	}
	if subscriptionID == "" {
		return nil
	}

	// Find user by subscription ID and mark as inactive
	filter := bson.M{"user.subscription.id": subscriptionID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.active":    false,
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}

	zap.S().Infof("Marked subscription %s as inactive due to payment failure", subscriptionID)
	return nil
}

// handleSubscriptionUpdated handles subscription updates
func (u User) handleSubscriptionUpdated(event stripe.Event) error {
	var sub stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &sub)
	if err != nil {
		return fmt.Errorf("failed to parse subscription: %v", err)
	}

	// Find user by subscription ID
	filter := bson.M{"user.subscription.id": sub.ID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.active":    sub.Status == "active",
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	// Map the Price ID to plan name and billing interval if items are present
	if len(sub.Items.Data) > 0 {
		priceID := sub.Items.Data[0].Price.ID
		plan, isAnnual := mapStripePriceIDToPlan(priceID)
		if plan != "unknown" {
			update["$set"].(bson.M)["user.subscription.plan"] = plan
			update["$set"].(bson.M)["user.subscription.isAnnual"] = isAnnual
			zap.S().Infof("Subscription %s plan changed to %s (annual: %v)", sub.ID, plan, isAnnual)
		}
	}

	// Handle cancelAt: set if subscription is scheduled to cancel, clear if reactivated
	if sub.CancelAt > 0 {
		cancelAtTime := time.Unix(sub.CancelAt, 0)
		update["$set"].(bson.M)["user.subscription.cancelAt"] = primitive.NewDateTimeFromTime(cancelAtTime)
	} else {
		update["$set"].(bson.M)["user.subscription.cancelAt"] = nil
	}

	for k, v := range subscriptionPeriodFields(&sub) {
		update["$set"].(bson.M)[k] = v
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}

	zap.S().Infof("Successfully updated subscription %s", sub.ID)
	return nil
}

// mapStripePriceIDToPlan maps a Stripe price ID to the plan name and whether
// it's annual billing. Accepts both V1 (original) and V2 (price-drop) price
// IDs so webhooks for grandfathered and migrated subs both resolve correctly.
// Empty env vars are skipped to avoid matching an empty incoming priceID.
func mapStripePriceIDToPlan(priceID string) (string, bool) {
	if priceID == "" {
		return "unknown", false
	}
	type tierEnv struct {
		plan     string
		annual   bool
		envNames []string
	}
	tiers := []tierEnv{
		{"base", false, []string{"STRIPE_BASE_MONTHLY_PRICE_ID", "STRIPE_BASE_V2_MONTHLY_PRICE_ID"}},
		{"base", true, []string{"STRIPE_BASE_ANNUAL_PRICE_ID", "STRIPE_BASE_V2_ANNUAL_PRICE_ID"}},
		{"premium", false, []string{"STRIPE_PREMIUM_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_V2_MONTHLY_PRICE_ID"}},
		{"premium", true, []string{"STRIPE_PREMIUM_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_V2_ANNUAL_PRICE_ID"}},
		{"premium_plus", false, []string{"STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID", "STRIPE_PREMIUM_PLUS_V2_MONTHLY_PRICE_ID"}},
		{"premium_plus", true, []string{"STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID", "STRIPE_PREMIUM_PLUS_V2_ANNUAL_PRICE_ID"}},
	}
	for _, t := range tiers {
		for _, env := range t.envNames {
			if v := os.Getenv(env); v != "" && v == priceID {
				return t.plan, t.annual
			}
		}
	}
	return "unknown", false
}

// handleSubscriptionDeleted handles subscription deletions
func (u User) handleSubscriptionDeleted(event stripe.Event) error {
	var sub stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &sub)
	if err != nil {
		return fmt.Errorf("failed to parse subscription: %v", err)
	}

	ctx := context.Background()

	// Find user by subscription ID first to get their user ID
	var user models.User
	err = u.DB.FindOne(ctx, bson.M{"user.subscription.id": sub.ID}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			zap.S().Warnf("No user found with subscription %s", sub.ID)
			return nil
		}
		return fmt.Errorf("failed to find user: %v", err)
	}

	// Default to "free" plan
	newPlan := "free"

	// Check if user has an active content creator entitlement for their personal plan
	if u.EntDB != nil {
		userObjID, convErr := primitive.ObjectIDFromHex(user.ID)
		if convErr == nil {
			entitlement, entErr := u.EntDB.FindOne(ctx, bson.M{
				"targetType": "user",
				"targetId":   userObjID,
				"active":     true,
			})
			if entErr == nil && entitlement != nil {
				// User has an active content creator personal plan entitlement
				// Fall back to "base" instead of "free"
				newPlan = entitlement.Plan
				zap.S().Infof("User %s has content creator entitlement, falling back to %s plan", user.ID, newPlan)
			}
		}
	}

	// Update user subscription
	filter := bson.M{"_id": user.ID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.active":    newPlan != "free", // active if they have entitlement
			"user.subscription.plan":      newPlan,
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}

	zap.S().Infof("Successfully deactivated subscription %s, user plan set to %s", sub.ID, newPlan)
	return nil
}

// handleSubscriptionTrialWillEnd handles trial ending notifications
func (u User) handleSubscriptionTrialWillEnd(event stripe.Event) error {
	var sub stripe.Subscription
	err := json.Unmarshal(event.Data.Raw, &sub)
	if err != nil {
		return fmt.Errorf("failed to parse subscription: %v", err)
	}

	// You can add logic here to send notifications to users about trial ending
	// For now, just log the event
	zap.S().Infof("Trial ending for subscription %s", sub.ID)
	return nil
}

// CancelSubscriptionHandler cancels a user's subscription
func (u User) CancelSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	rawBody, _ := ioutil.ReadAll(r.Body)
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if req.UserID == "" {
		config.ErrorStatus("user ID is required", http.StatusBadRequest, w, nil)
		return
	}

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(req.UserID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Retrieve the user from the database
	user := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus(fmt.Sprint("failed to find user ", req.UserID), http.StatusInternalServerError, w, err)
		return
	}

	// Update the subscription in Stripe to cancel at the end of the period
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	}
	sub, err := subscription.Update(user.Details.Subscription.ID, params)
	if err != nil {
		config.ErrorStatus(fmt.Sprint("Failed to cancel subscription", req.UserID), http.StatusInternalServerError, w, err)
		return
	}

	cancelAtTime := time.Unix(sub.CancelAt, 0)
	cancelAtPrimitive := primitive.NewDateTimeFromTime(cancelAtTime)

	filter := bson.M{"_id": uID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.cancelAt":  cancelAtPrimitive,
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update user subscription", http.StatusInternalServerError, w, err)
		return
	}

	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}
	rec.record(r.Context(), subscriptionEventInput{
		Provider:      "admin",
		EventType:     "admin_cancel_at_period_end",
		Store:         "STRIPE",
		UserIDHint:    req.UserID,
		TransactionID: user.Details.Subscription.ID,
		ExpiresAt:     &cancelAtTime,
		RawPayload:    rawBody,
		SourceIP:      r.RemoteAddr,
	})

	// Respond with the end date of the current billing cycle
	response := struct {
		Success bool   `json:"success"`
		EndDate int64  `json:"endDate"` // Unix timestamp
		Message string `json:"message"`
	}{
		Success: true,
		EndDate: sub.CancelAt,
		Message: "Subscription will be canceled at the end of the current billing cycle.",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UnsubscribeUserHandler unsubscribes a user
func (u User) UnsubscribeUserHandler(w http.ResponseWriter, r *http.Request) {
	rawBody, _ := ioutil.ReadAll(r.Body)
	var requestBody struct {
		UserID string `json:"userId"`
	}

	if err := json.Unmarshal(rawBody, &requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": userID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.plan":      "free",
			"user.subscription.active":    false,
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to unsubscribe user", http.StatusInternalServerError, w, err)
		return
	}

	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}
	rec.record(r.Context(), subscriptionEventInput{
		Provider:   "admin",
		EventType:  "admin_unsubscribe",
		UserIDHint: requestBody.UserID,
		Plan:       "free",
		RawPayload: rawBody,
		SourceIP:   r.RemoteAddr,
	})

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User unsubscribed successfully",
	})
}

func (u User) handleSuccessRedirect(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "Session ID is required", http.StatusBadRequest)
		return
	}

	deepLink := fmt.Sprintf("exp+police-cad-app://success?session_id=%s", sessionID)
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(fmt.Sprintf(`
        <html>
        <head>
            <meta http-equiv="refresh" content="0;url=%s">
            <script>
                window.location.href = "%s";
                setTimeout(function() {
                    document.getElementById('fallback').style.display = 'block';
                }, 1000);
            </script>
        </head>
        <body>
            <p>Redirecting back to the app...</p>
            <p id="fallback" style="display:none;">
                Payment Successful! If you are not redirected, 
                <a href="%s">click here to return to the app</a>.
            </p>
        </body>
        </html>
    `, deepLink, deepLink, deepLink)))
}

func (u User) handleCancelRedirect(w http.ResponseWriter, r *http.Request) {
	deepLink := "exp+police-cad-app://cancel"
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(fmt.Sprintf(`
        <html>
        <head>
            <meta http-equiv="refresh" content="0;url=%s">
            <script>
                window.location.href = "%s";
                setTimeout(function() {
                    document.getElementById('fallback').style.display = 'block';
                }, 1000);
            </script>
        </head>
        <body>
            <p>Redirecting back to the app...</p>
            <p id="fallback" style="display:none;">
                Payment Cancelled. If you are not redirected, 
                <a href="%s">click here to return to the app</a>.
            </p>
        </body>
        </html>
    `, deepLink, deepLink, deepLink)))
}

// AddUserNoteHandler adds a note to a user's notes array
func (u User) AddUserNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Parse the request body
	var newNote struct {
		Title     string `json:"title"`
		Content   string `json:"content"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&newNote); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate user ID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Create a new note with a generated ID
	note := models.Note{
		ID:        primitive.NewObjectID(),
		Title:     newNote.Title,
		Content:   newNote.Content,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	// First, check if the user exists and get their current notes
	var user models.User
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("user not found", http.StatusNotFound, w, err)
		return
	}

	// Update the user's notes in the database
	filter := bson.M{"_id": uID}
	
	// If notes is null or not an array, initialize it as an empty array first
	if user.Details.Notes == nil {
		_, err = u.DB.UpdateOne(context.Background(), filter, bson.M{"$set": bson.M{"user.notes": []models.Note{}}})
		if err != nil {
			config.ErrorStatus("failed to initialize notes array", http.StatusInternalServerError, w, err)
			return
		}
	}
	
	// Now push the new note
	update := bson.M{"$push": bson.M{"user.notes": note}}
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Note added successfully",
		"note":    note,
	})
}

// GetPrioritizedCommunitiesHandler retrieves communities sorted by subscription tier
// Deprecated: Use FetchPrioritizedCommunitiesHandler instead
func (u User) GetPrioritizedCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10 // Default limit
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 0 {
		Page = 0 // Default page
	}
	skip := int64(Page * Limit)
	limit64 := int64(Limit)

	// Define the subscription tier order
	// Define the subscription order for sorting
	subscriptionOrder := map[string]int{
		"elite":    4,
		"premium":  3,
		"standard": 2,
		"basic":    1,
		"":         0, // Default for no subscription
	}

	// Build the aggregation pipeline
	pipeline := mongo.Pipeline{
		// Filter for communities with visibility set to public
		{{"$match", bson.M{"community.visibility": "public"}}},

		// Add a numeric rank for subscription tiers
		{{"$addFields", bson.M{
			"subscriptionRank": bson.M{
				"$switch": bson.M{
					"branches": []bson.M{
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "elite"}}, "then": subscriptionOrder["elite"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "premium"}}, "then": subscriptionOrder["premium"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "standard"}}, "then": subscriptionOrder["standard"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "basic"}}, "then": subscriptionOrder["basic"]},
					},
					"default": subscriptionOrder[""],
				},
			},
		}}},

		// Sort by subscription rank (descending) and then by name (ascending)
		{{"$sort", bson.M{
			"subscriptionRank": -1, // Descending to prioritize higher tiers
			"community.name":   1,  // Ascending for alphabetical order within tiers
		}}},

		// Skip and limit for pagination
		{{"$skip", skip}},
		{{"$limit", limit64}},
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Execute the aggregation
	cursor, err := u.CDB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch prioritized communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	// Decode the results
	var communities []models.Community
	if err := cursor.All(ctx, &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Create the paginated response
	totalCount, _ := u.CDB.CountDocuments(ctx, bson.M{"community.visibility": "public"})
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       communities,
	}

	// Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}

// FetchPrioritizedCommunitiesHandler retrieves communities sorted by subscription tier with pagination and optimization
func (u User) FetchPrioritizedCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters
	Limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || Limit <= 0 {
		Limit = 10
	}
	Page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || Page < 0 {
		Page = 0
	}
	skip := int64(Page * Limit)
	limit64 := int64(Limit)

	// Subscription tier priority
	subscriptionOrder := map[string]int{
		"elite":    4,
		"premium":  3,
		"standard": 2,
		"basic":    1,
		"":         0,
	}

	// Aggregation pipeline
	pipeline := mongo.Pipeline{
		{{"$match", bson.M{"community.visibility": "public"}}},
		{{"$addFields", bson.M{
			"subscriptionRank": bson.M{
				"$switch": bson.M{
					"branches": []bson.M{
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "elite"}}, "then": subscriptionOrder["elite"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "premium"}}, "then": subscriptionOrder["premium"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "standard"}}, "then": subscriptionOrder["standard"]},
						{"case": bson.M{"$eq": []interface{}{"$community.subscription.plan", "basic"}}, "then": subscriptionOrder["basic"]},
					},
					"default": subscriptionOrder[""],
				},
			},
		}}},
		{{"$sort", bson.M{"subscriptionRank": -1, "community.name": 1}}},
		{{"$skip", skip}},
		{{"$limit", limit64}},
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	cursor, err := u.CDB.Aggregate(ctx, pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch prioritized communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	// Decode results
	var decodedCommunities []struct {
		ID        primitive.ObjectID `bson:"_id"`
		Community struct {
			Name                   string   `bson:"name"`
			ImageLink              string   `bson:"imageLink"`
			MembersCount           int      `bson:"membersCount"`
			Tags                   []string `bson:"tags"`
			PromotionalText        string   `bson:"promotionalText"`
			PromotionalDescription string   `bson:"promotionalDescription"`
			Subscription           struct {
				Active bool   `bson:"active"`
				Plan   string `bson:"plan"`
			} `bson:"subscription"`
		} `bson:"community"`
	}
	if err := cursor.All(ctx, &decodedCommunities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Self-heal stored membersCount with a live count from the users
	// collection (see liveMemberCounts for why).
	ids := make([]string, 0, len(decodedCommunities))
	for _, item := range decodedCommunities {
		ids = append(ids, item.ID.Hex())
	}
	liveCounts := liveMemberCounts(ctx, u.DB, ids)

	// Format response
	var responseData []map[string]interface{}
	for _, item := range decodedCommunities {
		membersCount := item.Community.MembersCount
		if live, ok := liveCounts[item.ID.Hex()]; ok {
			membersCount = live
		}
		responseData = append(responseData, map[string]interface{}{
			"_id":                    item.ID,
			"name":                   item.Community.Name,
			"imageLink":              item.Community.ImageLink,
			"membersCount":           membersCount,
			"tags":                   item.Community.Tags,
			"promotionalText":        item.Community.PromotionalText,
			"promotionalDescription": item.Community.PromotionalDescription,
			"subscription": map[string]interface{}{
				"active": item.Community.Subscription.Active,
				"plan":   item.Community.Subscription.Plan,
			},
		})
	}

	// Count total matching documents (use same ctx from aggregation)
	totalCount, _ := u.CDB.CountDocuments(ctx, bson.M{"community.visibility": "public"})

	// Return response
	response := map[string]interface{}{
		"page":       Page,
		"totalCount": totalCount,
		"data":       responseData,
		"limit":      Limit,
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// UpdateUserNoteHandler updates a specific note for a user
func (u User) UpdateUserNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	noteID := mux.Vars(r)["noteId"]

	// Parse the request body
	var updatedNote struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updatedNote); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate IDs
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	nID, err := primitive.ObjectIDFromHex(noteID)
	if err != nil {
		config.ErrorStatus("invalid note ID", http.StatusBadRequest, w, err)
		return
	}

	// Update the specific note
	filter := bson.M{"_id": uID, "user.notes._id": nID}
	update := bson.M{
		"$set": bson.M{
			"user.notes.$.title":     updatedNote.Title,
			"user.notes.$.content":   updatedNote.Content,
			"user.notes.$.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Note updated successfully"}`))
}

// DeleteUserNoteHandler deletes a specific note for a user
func (u User) DeleteUserNoteHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	noteID := mux.Vars(r)["noteId"]

	// Validate IDs
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	nID, err := primitive.ObjectIDFromHex(noteID)
	if err != nil {
		config.ErrorStatus("invalid note ID", http.StatusBadRequest, w, err)
		return
	}

	// Remove the specific note
	filter := bson.M{"_id": uID}
	update := bson.M{"$pull": bson.M{"user.notes": bson.M{"_id": nID}}}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete note", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Note deleted successfully"}`))
}

// UpdateUserSubscriptionHandler updates a user's subscription details
func (u User) UpdateUserSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	rawBody, _ := ioutil.ReadAll(r.Body)
	// Parse the request body to get the subscription details
	var subscriptionData struct {
		SubscriptionID string `json:"subscriptionId"`
		Plan           string `json:"plan"`
		IsAnnual       bool   `json:"isAnnual"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal(rawBody, &subscriptionData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Prepare the update document
	isActive := subscriptionData.Status == "active"
	update := bson.M{
		"$set": bson.M{
			"user.subscription.id":        subscriptionData.SubscriptionID,
			"user.subscription.plan":      subscriptionData.Plan,
			"user.subscription.isAnnual":  subscriptionData.IsAnnual,
			"user.subscription.active":    isActive,
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	// Update the user's subscription in the database
	filter := bson.M{"_id": uID}
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update user subscription", http.StatusInternalServerError, w, err)
		return
	}

	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}
	rec.record(r.Context(), subscriptionEventInput{
		Provider:      "admin",
		EventType:     "admin_update_subscription",
		UserIDHint:    userID,
		Plan:          subscriptionData.Plan,
		IsAnnual:      subscriptionData.IsAnnual,
		TransactionID: subscriptionData.SubscriptionID,
		RawPayload:    rawBody,
		SourceIP:      r.RemoteAddr,
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User subscription updated successfully"}`))
}

// DeactivateUserHandler deactivates a user's account
func (u User) DeactivateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Set the deactivation fields
	deactivationTime := primitive.NewDateTimeFromTime(time.Now())
	update := bson.M{
		"$set": bson.M{
			"user.isDeactivated": true,
			"user.deactivatedAt": deactivationTime,
			"user.restoreUntil":  primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, 30)), // 30 days to restore
		},
	}

	// Update the user's account in the database
	filter := bson.M{"_id": uID}
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to deactivate user account", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User account deactivated successfully"}`))
}

// FetchUserCommunitiesHandler retrieves a user's communities with pagination and filtering
func (u User) FetchUserCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	userID := mux.Vars(r)["userId"]
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 10
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	skip := (page - 1) * limit
	filter := r.URL.Query().Get("filter")

	// Convert the user ID to ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	// Use request context with timeout for proper trace tracking and timeout handling
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch the user document
	var user models.User
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Filter user's communities
	var (
		filteredCommunities []primitive.ObjectID
		totalFilteredCount  int
	)
	for _, community := range user.Details.Communities {
		matchesFilter := true
		if filter != "" {
			parts := strings.Split(filter, ":")
			if len(parts) >= 2 {
				matchesFilter = strings.Contains(community.Status, parts[1])
			} else {
				matchesFilter = false
			}
		}

		if matchesFilter {
			cID, _ := primitive.ObjectIDFromHex(community.CommunityID)
			filteredCommunities = append(filteredCommunities, cID)
			totalFilteredCount++
		}
	}

	// If no communities match the filter, return empty response
	if len(filteredCommunities) == 0 {
		response := map[string]interface{}{
			"data":       []interface{}{},
			"page":       page,
			"limit":      limit,
			"totalCount": 0,
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Build Mongo filter and fetch communities. CDB.Find/CountDocuments both
	// exclude pending-deletion communities; recompute totalCount from the
	// collection so the client's pagination terminates when soft-deleted
	// communities are stripped from results (otherwise data.length stays below
	// the user-array count forever and pagers loop).
	communityFilter := bson.M{"_id": bson.M{"$in": filteredCommunities}}
	if visibleCount, countErr := u.CDB.CountDocuments(ctx, communityFilter); countErr == nil {
		totalFilteredCount = int(visibleCount)
	}
	opts := options.Find().SetSkip(int64(skip)).SetLimit(int64(limit))
	cursor, err := u.CDB.Find(ctx, communityFilter, opts)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	// Decode all documents
	var decodedCommunities []struct {
		ID        primitive.ObjectID `bson:"_id"`
		Community struct {
			Name         string `bson:"name"`
			ImageLink    string `bson:"imageLink"`
			MembersCount int    `bson:"membersCount"`
			Subscription struct {
				Active bool `bson:"active"`
			} `bson:"subscription"`
			PromotionalText string `bson:"promotionalText"`
		} `bson:"community"`
	}
	if err := cursor.All(ctx, &decodedCommunities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Self-heal stored membersCount with a live count from the users
	// collection (see liveMemberCounts for why).
	ids := make([]string, 0, len(decodedCommunities))
	for _, item := range decodedCommunities {
		ids = append(ids, item.ID.Hex())
	}
	liveCounts := liveMemberCounts(ctx, u.DB, ids)

	// Transform into response format
	var communities []map[string]interface{}
	for _, item := range decodedCommunities {
		membersCount := item.Community.MembersCount
		if live, ok := liveCounts[item.ID.Hex()]; ok {
			membersCount = live
		}
		communities = append(communities, map[string]interface{}{
			"_id":             item.ID,
			"name":            item.Community.Name,
			"imageLink":       item.Community.ImageLink,
			"membersCount":    membersCount,
			"subscription":    item.Community.Subscription.Active,
			"promotionalText": item.Community.PromotionalText,
		})
	}

	// Return paginated response
	response := map[string]interface{}{
		"data":       communities,
		"page":       page,
		"limit":      limit,
		"totalCount": totalFilteredCount,
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// BoostCommunitiesHandler returns a user's communities with subscription data for the boost pricing page.
// Supports search by name and pagination. This is a dedicated endpoint that does not affect
// FetchUserCommunitiesHandler or CommunitiesByOwnerIDHandler.
func (u User) BoostCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]
	search := r.URL.Query().Get("search")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Fetch user document to get their community memberships
	var user models.User
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Collect approved community IDs
	var communityIDs []primitive.ObjectID
	for _, c := range user.Details.Communities {
		if strings.EqualFold(c.Status, "approved") {
			cID, parseErr := primitive.ObjectIDFromHex(c.CommunityID)
			if parseErr == nil {
				communityIDs = append(communityIDs, cID)
			}
		}
	}

	if len(communityIDs) == 0 {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
		return
	}

	// Build filter: must be in user's communities, optionally filter by name
	filter := bson.M{"_id": bson.M{"$in": communityIDs}}
	if search != "" {
		filter["community.name"] = bson.M{"$regex": regexp.QuoteMeta(search), "$options": "i"}
	}

	// Only fetch the fields the boost page needs
	projection := bson.M{
		"community.name":                       1,
		"community.subscription.active":        1,
		"community.subscription.plan":          1,
		"community.subscription.purchaseDate":  1,
		"community.subscription.expirationDate": 1,
		"community.subscription.durationMonths": 1,
	}

	opts := options.Find().SetLimit(int64(limit)).SetProjection(projection)
	cursor, err := u.CDB.Find(ctx, filter, opts)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	// Decode into lightweight struct matching frontend Community interface
	var results []struct {
		ID        primitive.ObjectID `json:"_id" bson:"_id"`
		Community struct {
			Name         string `json:"name" bson:"name"`
			Subscription struct {
				Active         bool   `json:"active" bson:"active"`
				Plan           string `json:"plan" bson:"plan"`
				PurchaseDate   string `json:"purchaseDate" bson:"purchaseDate"`
				ExpirationDate string `json:"expirationDate" bson:"expirationDate"`
				DurationMonths int    `json:"durationMonths" bson:"durationMonths"`
			} `json:"subscription" bson:"subscription"`
		} `json:"community" bson:"community"`
	}
	if err = cursor.All(ctx, &results); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	if results == nil {
		results = make([]struct {
			ID        primitive.ObjectID `json:"_id" bson:"_id"`
			Community struct {
				Name         string `json:"name" bson:"name"`
				Subscription struct {
					Active         bool   `json:"active" bson:"active"`
					Plan           string `json:"plan" bson:"plan"`
					PurchaseDate   string `json:"purchaseDate" bson:"purchaseDate"`
					ExpirationDate string `json:"expirationDate" bson:"expirationDate"`
					DurationMonths int    `json:"durationMonths" bson:"durationMonths"`
				} `json:"subscription" bson:"subscription"`
			} `json:"community" bson:"community"`
		}, 0)
	}

	b, err := json.Marshal(results)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// revenueCatEvent is the subset of a RevenueCat webhook event payload we care
// about. RevenueCat documents the full schema at
// https://www.revenuecat.com/docs/integrations/webhooks/event-types-and-fields
type revenueCatEvent struct {
	ID                    string
	AppUserID             string
	Type                  string
	Store                 string
	ProductID             string
	TransactionID         string
	OriginalTransactionID string
	PurchasedAt           *time.Time
	ExpiresAt             *time.Time
	PriceUSD              float64
	PriceLocal            float64
	Currency              string
	Environment           string
	PeriodType            string
}

// parseRevenueCatEvent extracts the typed event from the webhook body.
// Returns (event, nil) on success; (nil, err) on a structural problem.
func parseRevenueCatEvent(body map[string]interface{}) (*revenueCatEvent, error) {
	raw, ok := body["event"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or invalid event object")
	}
	ev := &revenueCatEvent{}
	if v, ok := raw["id"].(string); ok {
		ev.ID = v
	}
	if v, ok := raw["app_user_id"].(string); ok {
		ev.AppUserID = v
	} else if v, ok := body["app_user_id"].(string); ok {
		// Older RC payloads put it at the top level.
		ev.AppUserID = v
	}
	if v, ok := raw["type"].(string); ok {
		ev.Type = v
	}
	if v, ok := raw["store"].(string); ok {
		ev.Store = v
	}
	if v, ok := raw["product_id"].(string); ok {
		ev.ProductID = v
	}
	if v, ok := raw["transaction_id"].(string); ok {
		ev.TransactionID = v
	}
	if v, ok := raw["original_transaction_id"].(string); ok {
		ev.OriginalTransactionID = v
	}
	if v, ok := raw["currency"].(string); ok {
		ev.Currency = v
	}
	if v, ok := raw["environment"].(string); ok {
		ev.Environment = v
	}
	if v, ok := raw["period_type"].(string); ok {
		ev.PeriodType = v
	}
	if v, ok := raw["price"].(float64); ok {
		ev.PriceUSD = v
	}
	if v, ok := raw["price_in_purchased_currency"].(float64); ok {
		ev.PriceLocal = v
	}
	if v, ok := raw["purchased_at_ms"].(float64); ok && v > 0 {
		t := time.UnixMilli(int64(v)).UTC()
		ev.PurchasedAt = &t
	}
	if v, ok := raw["expiration_at_ms"].(float64); ok && v > 0 {
		t := time.UnixMilli(int64(v)).UTC()
		ev.ExpiresAt = &t
	}
	return ev, nil
}

// HandleRevenueCatWebhook handles RevenueCat webhook events for subscription
// management. Every delivery (including duplicates and unhandled types) is
// recorded to subscription_events for audit; the User document is mutated
// only for known event types. Idempotency is provided by a unique index on
// (provider, providerEventId) plus a pre-flight dedupe lookup, so RevenueCat
// retries are safe.
func (u User) HandleRevenueCatWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		config.ErrorStatus("failed to read webhook payload", http.StatusBadRequest, w, err)
		return
	}

	var webhookData map[string]interface{}
	if err := json.Unmarshal(payload, &webhookData); err != nil {
		config.ErrorStatus("failed to parse webhook payload", http.StatusBadRequest, w, err)
		return
	}

	ev, err := parseRevenueCatEvent(webhookData)
	if err != nil {
		config.ErrorStatus(fmt.Sprintf("invalid webhook event structure: %v", err), http.StatusBadRequest, w, err)
		return
	}

	zap.S().Infow("Received RevenueCat webhook event",
		"eventType", ev.Type,
		"eventId", ev.ID,
		"appUserId", ev.AppUserID,
		"store", ev.Store,
		"productId", ev.ProductID)

	rec := subscriptionEventRecorder{UserDB: u.DB, EventDB: u.SEDB}

	// Pre-flight dedupe: if we've already recorded this event id, return 200
	// OK without re-running the handler. The unique index on (provider,
	// providerEventId) is the source of truth and will catch the race.
	if ev.ID != "" && rec.isDuplicate(r.Context(), "revenuecat", ev.ID) {
		zap.S().Infow("duplicate RevenueCat event — skipping handler",
			"eventId", ev.ID,
			"eventType", ev.Type)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "duplicate event — already processed"}`))
		return
	}

	plan, isAnnual, _ := parseProductID(ev.ProductID)

	rr := rec.record(r.Context(), subscriptionEventInput{
		Provider:              "revenuecat",
		ProviderEventID:       ev.ID,
		EventType:             ev.Type,
		Store:                 ev.Store,
		UserIDHint:            ev.AppUserID,
		Plan:                  plan,
		IsAnnual:              isAnnual,
		ProductID:             ev.ProductID,
		TransactionID:         ev.TransactionID,
		OriginalTransactionID: ev.OriginalTransactionID,
		PriceUSD:              ev.PriceUSD,
		PriceLocal:            ev.PriceLocal,
		Currency:              ev.Currency,
		PurchasedAt:           ev.PurchasedAt,
		ExpiresAt:             ev.ExpiresAt,
		Environment:           ev.Environment,
		RawPayload:            payload,
		SourceIP:              r.RemoteAddr,
	})
	if rr.Duplicate {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "duplicate event — already processed"}`))
		return
	}

	switch ev.Type {
	case "INITIAL_PURCHASE", "RENEWAL", "NON_RENEWING_PURCHASE", "PRODUCT_CHANGE":
		err = u.handleRevenueCatActivePurchase(r.Context(), ev)
	case "UNCANCELLATION":
		err = u.handleRevenueCatUncancellation(r.Context(), ev)
	case "CANCELLATION":
		err = u.handleRevenueCatCancellation(r.Context(), ev)
	case "EXPIRATION":
		err = u.handleRevenueCatExpiration(r.Context(), ev)
	case "BILLING_ISSUE":
		err = u.handleRevenueCatBillingIssue(r.Context(), ev)
	default:
		zap.S().Infow("Unhandled RevenueCat event type — recorded but not applied",
			"eventType", ev.Type, "eventId", ev.ID)
	}

	if err != nil {
		zap.S().Errorw("Error handling RevenueCat webhook event",
			"eventType", ev.Type, "eventId", ev.ID, "error", err)
		config.ErrorStatus("failed to process webhook", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "RevenueCat webhook processed successfully"}`))
}

// rcUserFilter returns the Mongo filter used to find a user by RevenueCat's
// app_user_id. RC's app_user_id is the user document's _id stringified
// (see the mobile app: Purchases.configure({ appUserID: userId }) where
// userId is the Mongo _id hex). If the value isn't a valid ObjectID we
// return nil so callers can skip the update rather than match every
// document.
func rcUserFilter(appUserID string) bson.M {
	oid, err := primitive.ObjectIDFromHex(appUserID)
	if err != nil {
		return nil
	}
	return bson.M{"_id": oid}
}

// handleRevenueCatActivePurchase covers any event that results in an
// active subscription: INITIAL_PURCHASE, RENEWAL, NON_RENEWING_PURCHASE,
// PRODUCT_CHANGE. All of these write the full subscription state we can
// derive from the payload so the User document is a faithful mirror of
// the latest known truth.
func (u User) handleRevenueCatActivePurchase(ctx context.Context, ev *revenueCatEvent) error {
	filter := rcUserFilter(ev.AppUserID)
	if filter == nil {
		zap.S().Warnw("RevenueCat event has invalid app_user_id — skipping User update",
			"eventType", ev.Type, "appUserId", ev.AppUserID)
		return nil
	}

	set := bson.M{
		"user.subscription.active":    true,
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}
	if source := mapStoreToSource(ev.Store); source != "" {
		set["user.subscription.source"] = source
	}
	if ev.TransactionID != "" {
		set["user.subscription.id"] = ev.TransactionID
	}
	if plan, isAnnual, ok := parseProductID(ev.ProductID); ok {
		set["user.subscription.plan"] = plan
		set["user.subscription.isAnnual"] = isAnnual
	}
	if ev.PurchasedAt != nil {
		set["user.subscription.purchaseDate"] = ev.PurchasedAt.Format(time.RFC3339)
	}
	if ev.ExpiresAt != nil {
		set["user.subscription.expirationDate"] = ev.ExpiresAt.Format(time.RFC3339)
		set["user.subscription.currentPeriodEnd"] = primitive.NewDateTimeFromTime(*ev.ExpiresAt)
	}
	// New purchase / renewal clears any pending cancellation.
	set["user.subscription.cancelAt"] = nil

	if _, err := u.DB.UpdateOne(ctx, filter, bson.M{"$set": set}); err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}
	zap.S().Infow("Updated user subscription (active purchase)",
		"appUserId", ev.AppUserID, "eventType", ev.Type, "store", ev.Store)
	return nil
}

// handleRevenueCatCancellation marks the subscription as cancelled but
// preserves access until the period ends. We leave plan/source/id intact
// so the user document still describes what they bought.
func (u User) handleRevenueCatCancellation(ctx context.Context, ev *revenueCatEvent) error {
	filter := rcUserFilter(ev.AppUserID)
	if filter == nil {
		zap.S().Warnw("RevenueCat cancellation has invalid app_user_id — skipping User update",
			"appUserId", ev.AppUserID)
		return nil
	}
	set := bson.M{
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}
	if ev.ExpiresAt != nil {
		set["user.subscription.cancelAt"] = primitive.NewDateTimeFromTime(*ev.ExpiresAt)
	}
	if _, err := u.DB.UpdateOne(ctx, filter, bson.M{"$set": set}); err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}
	zap.S().Infow("Recorded RevenueCat cancellation (access kept until expiresAt)",
		"appUserId", ev.AppUserID, "expiresAt", ev.ExpiresAt)
	return nil
}

// handleRevenueCatUncancellation flips an unexpired pending-cancel back on.
func (u User) handleRevenueCatUncancellation(ctx context.Context, ev *revenueCatEvent) error {
	filter := rcUserFilter(ev.AppUserID)
	if filter == nil {
		return nil
	}
	_, err := u.DB.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"user.subscription.active":    true,
		"user.subscription.cancelAt":  nil,
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}})
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}
	zap.S().Infow("Updated user subscription to active (uncancellation)", "appUserId", ev.AppUserID)
	return nil
}

// handleRevenueCatExpiration marks the subscription as ended. Plan goes
// to "free" so feature gates respond correctly; we keep source / id /
// purchaseDate / expirationDate intact so the user doc remains a useful
// breadcrumb for diagnostics.
func (u User) handleRevenueCatExpiration(ctx context.Context, ev *revenueCatEvent) error {
	filter := rcUserFilter(ev.AppUserID)
	if filter == nil {
		return nil
	}
	_, err := u.DB.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"user.subscription.active":    false,
		"user.subscription.plan":      "free",
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}})
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}
	zap.S().Infow("Updated user subscription to inactive (expiration)", "appUserId", ev.AppUserID)
	return nil
}

// handleRevenueCatBillingIssue marks the sub inactive but does NOT
// downgrade the plan — RevenueCat may recover on next retry and we don't
// want to bounce the user through free in the meantime.
func (u User) handleRevenueCatBillingIssue(ctx context.Context, ev *revenueCatEvent) error {
	filter := rcUserFilter(ev.AppUserID)
	if filter == nil {
		return nil
	}
	_, err := u.DB.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
		"user.subscription.active":    false,
		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	}})
	if err != nil {
		return fmt.Errorf("failed to update user subscription: %v", err)
	}
	zap.S().Infow("Updated user subscription to inactive (billing issue)", "appUserId", ev.AppUserID)
	return nil
}

// UserResetPasswordHandler handles password reset for regular users
// This endpoint verifies the reset token and updates the user's password
func (u User) UserResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	token := strings.TrimSpace(req.Token)
	password := req.Password

	if token == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Token and password are required",
		})
		return
	}

	// Hash the token to compare with stored hash
	tokenHash := sha256.Sum256([]byte(token))
	hashedToken := hex.EncodeToString(tokenHash[:])

	// Use request context with timeout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find user with matching reset token that hasn't expired
	var user struct {
		ID      primitive.ObjectID `bson:"_id"`
		Details models.UserDetails `bson:"user"`
	}

	err := u.DB.FindOne(ctx, bson.M{
		"user.resetPasswordToken": hashedToken,
	}).Decode(&user)

	if err != nil {
		zap.S().Warnf("Password reset failed - token not found: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid or expired reset token",
		})
		return
	}

	// Check if token has expired
	var expiresAt time.Time
	switch exp := user.Details.ResetPasswordExpires.(type) {
	case time.Time:
		expiresAt = exp
	case primitive.DateTime:
		expiresAt = exp.Time()
	case string:
		parsedTime, parseErr := time.Parse(time.RFC3339, exp)
		if parseErr != nil {
			zap.S().Warnf("Failed to parse reset token expiry: %v", parseErr)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Invalid reset token",
			})
			return
		}
		expiresAt = parsedTime
	default:
		zap.S().Warnf("Unknown reset token expiry type: %T", exp)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid reset token",
		})
		return
	}

	if time.Now().After(expiresAt) {
		zap.S().Warnf("Password reset failed - token expired for user %s", user.ID.Hex())
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Reset token has expired",
		})
		return
	}

	// Hash the new password
	newHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		zap.S().Errorf("Failed to hash password: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Could not update password",
		})
		return
	}

	// Update user password and clear reset token
	_, err = u.DB.UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{
		"$set": bson.M{
			"user.password":             string(newHash),
			"user.resetPasswordToken":   "",
			"user.resetPasswordExpires": nil,
			"updatedAt":                 primitive.NewDateTimeFromTime(time.Now()),
		},
	})

	if err != nil {
		zap.S().Errorf("Failed to update password for user %s: %v", user.ID.Hex(), err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Could not update password",
		})
		return
	}

	zap.S().Infof("Password reset successful for user %s", user.ID.Hex())

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Password updated successfully",
	})
}

// SyncPasswordHandler syncs a user's password from the website to the API database
// This is called by the website after a password reset to ensure both databases are in sync
// Protected by server-to-server API token
func (u User) SyncPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Verify server-to-server API token
	authHeader := r.Header.Get("Authorization")
	expectedToken := os.Getenv("POLICE_CAD_API_TOKEN")
	if expectedToken == "" {
		zap.S().Error("POLICE_CAD_API_TOKEN not configured")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Server misconfigured",
		})
		return
	}

	// Check Bearer token
	if !strings.HasPrefix(authHeader, "Bearer ") || authHeader[7:] != expectedToken {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Unauthorized",
		})
		return
	}

	var req struct {
		Email        string `json:"email"`
		PasswordHash string `json:"passwordHash"`
		Password     string `json:"password"` // Plain password (preferred) - will be hashed by API
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))

	// Prefer plain password over pre-hashed (to ensure Go bcrypt compatibility)
	var passwordHash string
	if req.Password != "" {
		// Hash the plain password using Go's bcrypt
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if hashErr != nil {
			zap.S().Errorf("Failed to hash password: %v", hashErr)
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"error":   "Could not process password",
			})
			return
		}
		passwordHash = string(hash)
	} else if req.PasswordHash != "" {
		// Fall back to pre-hashed password (may have bcrypt compatibility issues)
		passwordHash = req.PasswordHash
	} else {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Email and password (or passwordHash) are required",
		})
		return
	}

	if email == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Email is required",
		})
		return
	}

	// Use request context with timeout
	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	// Find user by email
	var user struct {
		ID primitive.ObjectID `bson:"_id"`
	}

	err := u.DB.FindOne(ctx, bson.M{
		"user.email": email,
	}).Decode(&user)

	if err != nil {
		zap.S().Warnf("Password sync failed - user not found for email %s: %v", email, err)
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "User not found",
		})
		return
	}

	// Update user password
	result, err := u.DB.UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{
		"$set": bson.M{
			"user.password": passwordHash,
			"updatedAt":     primitive.NewDateTimeFromTime(time.Now()),
		},
	})

	if err != nil {
		zap.S().Errorf("Failed to sync password for user %s: %v", user.ID.Hex(), err)
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Could not update password",
		})
		return
	}

	zap.S().Infow("Password synced successfully",
		"userID", user.ID.Hex(),
		"email", email,
		"matchedCount", result.MatchedCount,
		"modifiedCount", result.ModifiedCount)

	// Invalidate the auth cache for this user so they must re-authenticate with new password
	if err := api.InvalidateAuthCache(email); err != nil {
		zap.S().Warnw("Failed to invalidate auth cache (password still updated)",
			"email", email,
			"error", err)
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Password synced successfully",
	})
}

// RegisterPushTokenHandler registers or updates an Expo push token for a user
func (u User) RegisterPushTokenHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID   string `json:"userId"`
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.Token == "" || request.UserID == "" {
		config.ErrorStatus("token and userId are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	now := primitive.NewDateTimeFromTime(time.Now())

	// Upsert: if this token already exists for this user, update it; otherwise insert
	filter := bson.M{"userId": request.UserID, "token": request.Token}
	update := bson.M{
		"$set": bson.M{
			"userId":    request.UserID,
			"token":     request.Token,
			"platform":  request.Platform,
			"updatedAt": now,
		},
		"$setOnInsert": bson.M{
			"createdAt": now,
		},
	}

	opts := options.Update().SetUpsert(true)
	_, err := u.PTDB.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		config.ErrorStatus("failed to register push token", http.StatusInternalServerError, w, err)
		return
	}

	zap.S().Infof("Push token registered for user %s (platform: %s)", request.UserID, request.Platform)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Push token registered successfully",
	})
}

// RemovePushTokenHandler removes an Expo push token for a user (e.g., on logout)
func (u User) RemovePushTokenHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID string `json:"userId"`
		Token  string `json:"token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.UserID == "" {
		config.ErrorStatus("userId is required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	var filter bson.M
	if request.Token != "" {
		// Remove specific token
		filter = bson.M{"userId": request.UserID, "token": request.Token}
		err := u.PTDB.DeleteOne(ctx, filter)
		if err != nil {
			config.ErrorStatus("failed to remove push token", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Remove all tokens for user
		filter = bson.M{"userId": request.UserID}
		_, err := u.PTDB.DeleteMany(ctx, filter)
		if err != nil {
			config.ErrorStatus("failed to remove push tokens", http.StatusInternalServerError, w, err)
			return
		}
	}

	zap.S().Infof("Push token(s) removed for user %s", request.UserID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Push token(s) removed successfully",
	})
}

// DismissTutorialHandler adds a tutorial key to the user's dismissedTutorials array.
// PUT /api/v1/user/{userId}/dismiss-tutorial
func (u User) DismissTutorialHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	var body struct {
		TutorialKey string `json:"tutorialKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TutorialKey == "" {
		config.ErrorStatus("tutorialKey is required", http.StatusBadRequest, w, fmt.Errorf("invalid body"))
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err = u.DB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$addToSet": bson.M{"user.dismissedTutorials": body.TutorialKey},
	})
	if err != nil {
		config.ErrorStatus("failed to dismiss tutorial", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}

// SetAlertSoundsEnabledHandler toggles the per-user master switch for CAD alert
// sounds (new-call/warrant/attach tones). Defaults off; users opt in.
// PUT /api/v1/user/{userId}/alert-sounds-enabled  body: {"enabled": bool}
func (u User) SetAlertSoundsEnabledHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		config.ErrorStatus("invalid body", http.StatusBadRequest, w, err)
		return
	}

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	_, err = u.DB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$set": bson.M{"user.alertSoundsEnabled": body.Enabled},
	})
	if err != nil {
		config.ErrorStatus("failed to update alert sounds setting", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "enabled": body.Enabled})
}

// ChangeEmailHandler is the legacy v1 email-change endpoint (password-only verification). Kept
// in place for backward compatibility while clients migrate to the verified v2 flow on
// PendingVerification.{Request,Confirm}EmailChangeHandler. Plan to delete after migration.
func (u User) ChangeEmailHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["user_id"]

	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	var req struct {
		NewEmail        string `json:"newEmail"`
		CurrentPassword string `json:"currentPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	newEmail := strings.TrimSpace(strings.ToLower(req.NewEmail))
	currentPassword := req.CurrentPassword
	if newEmail == "" || currentPassword == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "New email and current password are required"})
		return
	}

	ctx, cancel := api.WithQueryTimeout(r.Context())
	defer cancel()

	existingUser := models.User{}
	err = u.DB.FindOne(ctx, bson.M{"_id": uID}).Decode(&existingUser)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "User not found"})
			return
		}
		config.ErrorStatus("failed to find user", http.StatusInternalServerError, w, err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(existingUser.Details.Password), []byte(currentPassword)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid password"})
		return
	}

	var conflictUser models.User
	err = u.DB.FindOne(ctx, bson.M{
		"user.email": newEmail,
		"_id":        bson.M{"$ne": uID},
	}).Decode(&conflictUser)
	if err == nil {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "Email already in use"})
		return
	}
	if err != mongo.ErrNoDocuments {
		config.ErrorStatus("failed to check email uniqueness", http.StatusInternalServerError, w, err)
		return
	}

	if _, err := u.DB.UpdateOne(ctx, bson.M{"_id": uID}, bson.M{
		"$set": bson.M{
			"user.email":     newEmail,
			"user.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}); err != nil {
		config.ErrorStatus("failed to update email", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Email updated successfully"})
}
