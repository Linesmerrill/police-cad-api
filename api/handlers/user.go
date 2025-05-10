package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"github.com/stripe/stripe-go/v82"
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
	DB  databases.UserDatabase
	CDB databases.CommunityDatabase
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

	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": cID}).Decode(&dbResp)
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

	cursor, err := u.DB.Find(context.Background(), bson.M{"user.activeCommunity": commID})
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
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

	filter := bson.M{"_id": bson.M{"$in": objectIDs}}
	cursor, err := u.DB.Find(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to fetch users", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
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
		usernameHash := sha256.Sum256([]byte(email))

		// fetch email & pass from db
		dbEmailResp := models.User{}
		err := u.DB.FindOne(context.Background(), bson.M{"user.email": email}).Decode(&dbEmailResp)
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

	w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
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

	// check if the user already exists
	existingUser := models.User{}
	_ = u.DB.FindOne(context.Background(), bson.M{"user.email": user.Email}).Decode(&existingUser)
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
	_, err = u.DB.InsertOne(context.Background(), user)
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

	// check if the user already exists
	existingUser := models.User{}
	_ = u.DB.FindOne(context.Background(), bson.M{"user.email": user.Email}).Decode(&existingUser)
	if existingUser.ID != "" {
		config.ErrorStatus("email already exists", http.StatusConflict, w, fmt.Errorf("duplicate email"))
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UsersDiscoverPeopleHandler returns a list of users that we suggest to the user to follow
func (u User) UsersDiscoverPeopleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse query parameters
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		config.ErrorStatus("query param userId is required", http.StatusBadRequest, w, fmt.Errorf("query param userId is required"))
		return
	}

	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Default limit
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default page
	}
	skip := (page - 1) * limit

	// Convert userID to ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("invalid userId", http.StatusBadRequest, w, err)
		return
	}

	// Aggregation pipeline to get approved and pending friend IDs
	pipeline := []bson.M{
		{
			"$match": bson.M{"_id": uID},
		},
		{
			"$unwind": bson.M{
				"path":                       "$user.friends",
				"preserveNullAndEmptyArrays": true,
			},
		},
		{
			"$match": bson.M{
				"$or": []bson.M{
					{"user.friends.status": "approved"},
					{"user.friends.status": "pending"},
				},
			},
		},
		{
			"$group": bson.M{
				"_id":       nil,
				"friendIDs": bson.M{"$addToSet": "$user.friends.friend_id"},
			},
		},
	}

	// Execute the pipeline to get friend IDs
	cursor, err := u.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch friends", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	// Define the result struct
	type friendResult struct {
		FriendIDs []string `bson:"friendIDs"`
	}
	var results []friendResult
	if err = cursor.All(context.Background(), &results); err != nil {
		config.ErrorStatus("failed to decode friends", http.StatusInternalServerError, w, err)
		return
	}

	// Extract friendIDs from results
	friendIDs := []string{}
	if len(results) > 0 {
		friendIDs = results[0].FriendIDs
	}
	if friendIDs == nil {
		friendIDs = []string{}
	}

	// Convert friend IDs to ObjectIDs
	friendObjectIDs := make([]primitive.ObjectID, 0, len(friendIDs))
	for _, fid := range friendIDs {
		if oid, err := primitive.ObjectIDFromHex(fid); err == nil {
			friendObjectIDs = append(friendObjectIDs, oid)
		}
	}

	// Pipeline to find random users excluding friends and the current user
	pipeline = []bson.M{
		{
			"$match": bson.M{
				"_id": bson.M{
					"$nin": append(friendObjectIDs, uID), // Exclude friends and current user
				},
			},
		},
		{
			"$sample": bson.M{"size": limit}, // Randomly select 'limit' users
		},
		{
			"$skip": skip, // Pagination: skip for the current page
		},
		{
			"$limit": limit, // Pagination: limit to requested size
		},
	}

	// Execute the aggregation
	cursor, err = u.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to get discover people recommendations", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	// Decode the results
	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
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
		"page":  page,
		"limit": limit,
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

	// Find the user by userId
	user := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
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

	community, err := u.CDB.FindOne(context.Background(), bson.M{"_id": cID})
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

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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

	// Fetch user details for each friend
	var detailedFriends []map[string]interface{}
	for _, friend := range paginatedFriends {
		fID, err := primitive.ObjectIDFromHex(friend.FriendID)
		if err != nil {
			continue
		}
		friendDetails := models.User{}
		err = u.DB.FindOne(context.Background(), bson.M{"_id": fID}).Decode(&friendDetails)
		if err != nil {
			continue
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

	// Retrieve the user's details
	filter := bson.M{"_id": uID}
	user := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&user)
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
		_, err = u.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's friends", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Check if the friend already exists
		existingFriend := models.User{}
		err := u.DB.FindOne(context.Background(), bson.M{"_id": uID, "user.friends.friend_id": friend.FriendID}).Decode(&existingFriend)
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

		_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
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

	// Perform blocked user check only if the notification type is "friend_request"
	if notification.Type == "friend_request" {
		nID, err := primitive.ObjectIDFromHex(notification.SentToID)
		if err != nil {
			config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
			return
		}

		filter := bson.M{"_id": nID}
		dbResp := models.User{}
		err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
	if err != nil {
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the notifications array is nil or empty
	if dbResp.Details.Notifications == nil || len(dbResp.Details.Notifications) == 0 {
		update := bson.M{
			"$set": bson.M{"user.notifications": []models.Notification{newNotification}},
		}
		_, err = u.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's notifications", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		update := bson.M{"$push": bson.M{"user.notifications": newNotification}}
		opts := options.Update().SetUpsert(false)

		_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
		if err != nil {
			config.ErrorStatus("failed to create notification", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "notification created successfully"}`))
}

// GetUserNotificationsHandler returns all notifications for a user
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

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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
		err = u.DB.FindOne(context.Background(), bson.M{"_id": senderID}).Decode(&sender)
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

	// Update the user's friend status
	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update friend status", http.StatusInternalServerError, w, err)
		return
	}

	// Update the friend's friend status
	friendFilter := bson.M{"_id": fID}
	friendResp := models.User{}
	err = u.DB.FindOne(context.Background(), friendFilter).Decode(&friendResp)
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
	_, err = u.DB.UpdateOne(context.Background(), friendFilter, friendUpdate)
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

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
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

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
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

	filter := bson.M{"_id": uID}
	dbResp := models.User{}
	err = u.DB.FindOne(context.Background(), filter).Decode(&dbResp)
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

	friendFilter := bson.M{"_id": fID}
	friendResp := models.User{}
	err = u.DB.FindOne(context.Background(), friendFilter).Decode(&friendResp)
	if err != nil {
		config.ErrorStatus("failed to get friend by ID", http.StatusNotFound, w, err)
		return
	}

	userFilter := bson.M{"_id": uID}
	userResp := models.User{}
	err = u.DB.FindOne(context.Background(), userFilter).Decode(&userResp)
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
func (u User) GetUserCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	userID := mux.Vars(r)["userId"]

	// Convert the user ID to a primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	user := models.User{}

	// Find the user by ID
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}

	// Extract the communities from the user
	communities := user.Details.Communities
	if communities == nil {
		communities = []models.UserCommunity{}
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

	// Update the user's lastAccessedCommunity details
	filter := bson.M{"_id": uID}
	update := bson.M{"$set": bson.M{
		"user.lastAccessedCommunity.communityID": request.CommunityID,
		"user.lastAccessedCommunity.createdAt":   createdAtPrimitive,
	}}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
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

	user := models.User{}

	// Find the user by ID
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
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
				config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
				return
			}
			communityObjectIDs = append(communityObjectIDs, objID)
		}
	}

	if communityObjectIDs == nil {
		communityObjectIDs = []primitive.ObjectID{}
	}

	// Find communities that the user does not belong to and are public
	filter := bson.M{
		"_id":                  bson.M{"$nin": communityObjectIDs},
		"community.visibility": "public",
	}
	opt := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit)).SetSort(bson.M{"$natural": 1})

	cursor, err := u.CDB.Find(context.Background(), filter, opt)
	if err != nil {
		config.ErrorStatus("failed to find communities", http.StatusInternalServerError, w, err)
		return
	}

	// If the cursor comes back as nil, that means there are no public communities - so we can just return an empty array
	defer cursor.Close(context.Background())

	var communities []models.Community

	if err = cursor.All(context.Background(), &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
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
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Check for the migration query parameter
	migration := r.URL.Query().Get("migration") == "true"

	// Convert the user ID to primitive.ObjectID
	uID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the user document
	filter := bson.M{"_id": uID}
	var user models.User
	err = u.DB.FindOne(context.Background(), filter).Decode(&user)
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
			_, err = u.DB.UpdateOne(context.Background(), filter, update)
			if err != nil {
				config.ErrorStatus("failed to initialize communities during migration", http.StatusInternalServerError, w, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message": "Community added successfully during migration"}`))
			return
		}
		config.ErrorStatus("failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}

	// Handle communities array based on its length
	newCommunity := models.UserCommunity{
		ID:          primitive.NewObjectID().Hex(),
		CommunityID: requestBody.CommunityID,
		Status:      requestBody.Status,
	}
	if user.Details.Communities == nil || len(user.Details.Communities) == 0 {
		// Initialize communities array and insert the first record
		update := bson.M{
			"$set": bson.M{"user.communities": []models.UserCommunity{newCommunity}},
		}
		_, err = u.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to initialize communities", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Insert the new community into the existing array
		update := bson.M{"$push": bson.M{"user.communities": newCommunity}}
		_, err = u.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to add community", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Convert the user ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Increment the membersCount in the community document
	communityFilter := bson.M{"_id": cID}
	communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": 1}}
	err = u.CDB.UpdateOne(context.Background(), communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to increment community membersCount", http.StatusInternalServerError, w, err)
		return
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

	user := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Check if the communities array is nil or empty
	if user.Details.Communities == nil || len(user.Details.Communities) == 0 {
		pendingRequest := models.UserCommunity{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: requestBody.CommunityID,
			Status:      "pending",
		}
		update := bson.M{
			"$set": bson.M{"user.communities": []models.UserCommunity{pendingRequest}},
		}
		_, err = u.DB.UpdateOne(context.Background(), bson.M{"_id": uID}, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's communities", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Check if the communityId already exists in the user's pending community requests
		existingUser := models.User{}
		err := u.DB.FindOne(context.Background(), bson.M{
			"_id": uID,
			"user.communities": bson.M{
				"$elemMatch": bson.M{
					"communityId": requestBody.CommunityID,
				},
			},
		}).Decode(&existingUser)
		if err == nil && len(existingUser.Details.Communities) > 0 {
			config.ErrorStatus("community request already exists", http.StatusConflict, w, fmt.Errorf("community request already exists"))
			return
		}

		// Create a new pending community request object
		pendingRequest := models.UserCommunity{
			ID:          primitive.NewObjectID().Hex(),
			CommunityID: requestBody.CommunityID,
			Status:      "pending",
		}

		// Update the user's pending community requests array
		filter := bson.M{"_id": uID}
		update := bson.M{"$addToSet": bson.M{"user.communities": pendingRequest}} // $addToSet ensures no duplicates
		_, err = u.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to update user's pending community requests", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Pending community request added successfully"}`))
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

	// Update the user's communities array to remove the specified community
	userFilter := bson.M{"_id": uID}
	userUpdate := bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": requestBody.CommunityID}}}
	_, err = u.DB.UpdateOne(context.Background(), userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove community from user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Find the community by community ID and decrement the membersCount
	communityFilter := bson.M{"_id": cID}
	communityUpdate := bson.M{"$inc": bson.M{"community.membersCount": -1}}
	err = u.CDB.UpdateOne(context.Background(), communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to decrement community membersCount", http.StatusInternalServerError, w, err)
		return
	}
	community, err := u.CDB.FindOne(context.Background(), communityFilter)

	// Iterate through the roles and remove the user ID from the members array
	for _, role := range community.Details.Roles {
		roleFilter := bson.M{"_id": cID, "community.roles._id": role.ID, "community.roles.members": userID}
		roleUpdate := bson.M{"$pull": bson.M{"community.roles.$.members": userID}}
		err := u.CDB.UpdateOne(context.Background(), roleFilter, roleUpdate)
		if err != nil {
			config.ErrorStatus("failed to remove user from role members", http.StatusInternalServerError, w, err)
			return
		}
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
	_, err = u.DB.UpdateOne(context.Background(), userFilter, userUpdate)
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
	err = u.CDB.UpdateOne(context.Background(), communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to update community ban list", http.StatusInternalServerError, w, err)
		return
	}

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

	// Set the updatedAt field to the current time
	updatedFields["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Create an update document targeting the internal user object
	update := bson.M{}
	for key, value := range updatedFields {
		update["user."+key] = value
	}

	// Update the user in the database
	filter := bson.M{"_id": uID}
	_, err = u.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update})
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

	// Retrieve the user's friends array
	user := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
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
		_, err = u.DB.UpdateOne(context.Background(), bson.M{"_id": uID}, update)
		if err != nil {
			config.ErrorStatus("failed to initialize user's friends", http.StatusInternalServerError, w, err)
			return
		}
	} else {
		// Check if the friendId exists in the user's friends list
		filter := bson.M{"_id": uID, "user.friends.friend_id": requestBody.FriendID}
		update := bson.M{"$set": bson.M{"user.friends.$.status": "blocked"}}
		result, err := u.DB.UpdateOne(context.Background(), filter, update)
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
			_, err = u.DB.UpdateOne(context.Background(), bson.M{"_id": uID}, update)
			if err != nil {
				config.ErrorStatus("failed to insert new friend with status blocked", http.StatusInternalServerError, w, err)
				return
			}
		}
	}

	// Remove the userId from the friendId's friends list
	friendFilter := bson.M{"_id": fID}
	friendUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.UserID}}}
	_, err = u.DB.UpdateOne(context.Background(), friendFilter, friendUpdate)
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

	// Remove the friendId from the user's friends list
	filter := bson.M{"_id": uID}
	update := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.FriendID}}}
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
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

	filter := bson.M{"_id": uID}
	update := bson.M{"$set": bson.M{"user.isOnline": requestBody.IsOnline}}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
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

	// Remove the friend from the user's friends list
	userFilter := bson.M{"_id": uID}
	userUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.FriendID}}}
	_, err = u.DB.UpdateOne(context.Background(), userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove friend from user's friends list", http.StatusInternalServerError, w, err)
		return
	}

	// Remove the user from the friend's friends list
	friendFilter := bson.M{"_id": fID}
	friendUpdate := bson.M{"$pull": bson.M{"user.friends": bson.M{"friend_id": requestBody.UserID}}}
	_, err = u.DB.UpdateOne(context.Background(), friendFilter, friendUpdate)
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

	// Find the community by ID
	community, err := u.CDB.FindOne(context.Background(), bson.M{"_id": cID})
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

	// Add the user to the department's members list with status "pending"
	member := models.MemberStatus{
		UserID: userID,
		Status: "pending",
	}
	department.Members = append(department.Members, member)

	// Update the community in the database
	update := bson.M{"$set": bson.M{"community.departments": community.Details.Departments}}
	err = u.CDB.UpdateOne(context.Background(), bson.M{"_id": cID}, update)
	if err != nil {
		config.ErrorStatus("failed to update community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User added to department with pending status successfully"}`))
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

	// userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	// if err != nil {
	// 	config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
	// 	return
	// }

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

	// filter := bson.M{"_id": userID}
	// update := bson.M{
	// 	"$set": bson.M{
	// 		"user.subscription.plan":      requestBody.Tier,
	// 		"user.subscription.isAnnual":  requestBody.IsAnnual,
	// 		"user.subscription.active":    true,
	// 		"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
	// 	},
	// }
	//
	// _, err = u.DB.UpdateOne(context.Background(), filter, update)
	// if err != nil {
	// 	config.ErrorStatus("failed to subscribe user", http.StatusInternalServerError, w, err)
	// 	return
	// }

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
			priceID = os.Getenv("STRIPE_BASE_MONTHLY_PRICE_ID")
		} else {
			priceID = os.Getenv("STRIPE_BASE_ANNUAL_PRICE_ID")
		}
	case "premium":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_PREMIUM_MONTHLY_PRICE_ID")
		} else {
			priceID = os.Getenv("STRIPE_PREMIUM_ANNUAL_PRICE_ID")
		}
	case "premium_plus":
		if billingInterval == "monthly" {
			priceID = os.Getenv("STRIPE_PREMIUM_PLUS_MONTHLY_PRICE_ID")
		} else {
			priceID = os.Getenv("STRIPE_PREMIUM_PLUS_ANNUAL_PRICE_ID")
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
		// http.Error(w, fmt.Sprintf("Price ID for tier %s and billing interval %s is not set", req.Tier, req.BillingInterval), http.StatusInternalServerError)
		return nil, fmt.Errorf("price ID for tier %s and billing interval %s is not set", c.Tier, c.BillingInterval)
	}

	successURL := fmt.Sprintf("%v/api/v1/success?session_id={CHECKOUT_SESSION_ID}", os.Getenv("BASE_URL"))
	cancelURL := fmt.Sprintf("%v/api/v1/cancel", os.Getenv("BASE_URL"))
	if os.Getenv("URL_MODE") == "testing" {
		successURL = fmt.Sprintf("http://%v/api/v1/success?session_id={CHECKOUT_SESSION_ID}", os.Getenv("BASE_URL"))
		cancelURL = fmt.Sprintf("http://%v/api/v1/cancel", os.Getenv("BASE_URL"))
	}

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

// SubscribeUserHandler subscribes a user to a specific tier
func (u User) SubscribeUserHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID         string `json:"userId"`
		SubscriptionID string `json:"subscriptionId"`
		Status         string `json:"status"`
		Tier           string `json:"tier"`
		IsAnnual       bool   `json:"isAnnual"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	userID, err := primitive.ObjectIDFromHex(requestBody.UserID)
	if err != nil {
		config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
		return
	}

	isActive := requestBody.Status == "active"

	filter := bson.M{"_id": userID}
	update := bson.M{
		"$set": bson.M{
			"user.subscription.id":        requestBody.SubscriptionID,
			"user.subscription.plan":      requestBody.Tier,
			"user.subscription.isAnnual":  requestBody.IsAnnual,
			"user.subscription.active":    isActive,
			"user.subscription.createdAt": primitive.NewDateTimeFromTime(time.Now()),
			"user.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to subscribe user", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "User subscribed successfully",
	})
}

// HandleWebhook handles Stripe webhook events
// Note: Delivery Delays
// Most webhooks are usually delivered within 5 to 60 seconds of the event occurring -
// **cancellation events usually are delivered within 2hrs** of the user cancelling their subscription.
// You should be aware of these delivery times when designing your app.
func (u User) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		config.ErrorStatus("failed to read webhook payload", http.StatusBadRequest, w, err)
		return
	}

	// Parse the RevenueCat webhook payload
	var event struct {
		EventType      string `json:"event_type"`
		SubscriberID   string `json:"subscriber_id"`
		CancellationAt string `json:"cancellation_at"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		config.ErrorStatus("failed to parse webhook payload", http.StatusBadRequest, w, err)
		return
	}

	// Handle cancellation event
	if event.EventType == "CANCELLATION" {
		cancellationTime, err := time.Parse(time.RFC3339, event.CancellationAt)
		if err != nil {
			config.ErrorStatus("invalid cancellation time format", http.StatusBadRequest, w, err)
			return
		}

		// Update the user's subscription to reflect pending cancellation
		_, err = u.DB.UpdateOne(
			context.Background(),
			bson.M{"subscription.subscriptionId": event.SubscriberID},
			bson.M{"$set": bson.M{
				"subscription.active":   true,
				"subscription.cancelAt": primitive.NewDateTimeFromTime(cancellationTime),
			}},
		)
		if err != nil {
			config.ErrorStatus("failed to update user subscription", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Webhook processed successfully"}`))
}

// CancelSubscriptionHandler cancels a user's subscription
func (u User) CancelSubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	var requestBody struct {
		UserID string `json:"userId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
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

	// Update the user's notes in the database
	filter := bson.M{"_id": uID}
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

	// Execute the aggregation
	cursor, err := u.CDB.Aggregate(context.TODO(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch prioritized communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.TODO())

	// Decode the results
	var communities []models.Community
	if err := cursor.All(context.TODO(), &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Create the paginated response
	totalCount, _ := u.CDB.CountDocuments(context.TODO(), bson.M{"community.visibility": "public"})
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       communities,
	}

	// Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
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

	// Parse the request body to get the subscription details
	var subscriptionData struct {
		SubscriptionID string `json:"subscriptionId"`
		Plan           string `json:"plan"`
		IsAnnual       bool   `json:"isAnnual"`
		Status         string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&subscriptionData); err != nil {
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
