package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	// Retrieve the user's friends list
	user := models.User{}
	err = u.DB.FindOne(context.Background(), bson.M{"_id": uID}).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's friends", http.StatusInternalServerError, w, err)
		return
	}

	// Extract the list of approved friends' IDs
	var approvedFriendIDs []primitive.ObjectID
	if user.Details.Friends != nil {
		for _, friend := range user.Details.Friends {
			if friend.Status == "approved" {
				fID, err := primitive.ObjectIDFromHex(friend.FriendID)
				if err == nil {
					approvedFriendIDs = append(approvedFriendIDs, fID)
				}
			}
		}
	}

	// Ensure approvedFriendIDs is initialized
	if approvedFriendIDs == nil {
		approvedFriendIDs = []primitive.ObjectID{}
	}

	// Modify the pipeline to exclude approved friends
	pipeline := []bson.M{
		{"$match": bson.M{"_id": bson.M{"$ne": uID, "$nin": approvedFriendIDs}}},
		{"$sample": bson.M{"size": 4}},
	}

	cursor, err := u.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to get discover people recommendations", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	err = cursor.All(context.Background(), &users)
	if err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

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

	// Marshal the community IDs to JSON
	b, err := json.Marshal(communities)
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

	if migration {
		// Handle migration logic
		filter := bson.M{"_id": uID}
		newCommunity := models.UserCommunity{
			CommunityID: requestBody.CommunityID,
			Status:      requestBody.Status,
		}

		// Ensure the `communities` array exists and add the new community
		update := bson.M{
			"$setOnInsert": bson.M{"user.communities": []models.UserCommunity{}},
			"$push":        bson.M{"user.communities": newCommunity},
		}
		opts := options.Update().SetUpsert(true)

		_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
		if err != nil {
			config.ErrorStatus("failed to add community during migration", http.StatusInternalServerError, w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"message": "Community added successfully during migration"}`))
		return
	}

	// Default behavior: Find and update pending community request
	filter := bson.M{
		"_id": uID,
		"user.communities": bson.M{
			"$elemMatch": bson.M{
				"communityId": requestBody.CommunityID,
				"status":      "pending",
			},
		},
	}
	update := bson.M{
		"$set": bson.M{
			"user.communities.$.status": requestBody.Status,
		},
	}

	result, err := u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update community request", http.StatusInternalServerError, w, err)
		return
	}

	if result.MatchedCount == 0 {
		config.ErrorStatus("no pending community request found", http.StatusNotFound, w, fmt.Errorf("no pending community request found"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community request updated successfully"}`))
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

	// Find the community by community ID
	communityFilter := bson.M{"_id": cID}
	community, err := u.CDB.FindOne(context.Background(), communityFilter)
	if err != nil {
		config.ErrorStatus("failed to find community by ID", http.StatusNotFound, w, err)
		return
	}

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

// SubscribeUserHandler subscribes a user to a specific tier
func (u User) SubscribeUserHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID   string `json:"userId"`
		Tier     string `json:"tier"`
		IsAnnual bool   `json:"isAnnual"`
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
			"user.subscription.plan":      requestBody.Tier,
			"user.subscription.isAnnual":  requestBody.IsAnnual,
			"user.subscription.active":    true,
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
