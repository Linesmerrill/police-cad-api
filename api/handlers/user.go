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

	dbResp, err := u.DB.FindOne(context.Background(), bson.M{"_id": cID})
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
func (u User) UsersFindAllHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["active_community_id"]

	zap.S().Debugf("active_community_id: %v", commID)

	dbResp, err := u.DB.Find(context.Background(), bson.M{"user.activeCommunity": commID})
	if err != nil {
		config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
		return
	}
	// Because the frontend requires that the data elements inside models.User exist, if
	// len == 0 then we will just return an empty data object
	if len(dbResp) == 0 {
		dbResp = []models.User{}
	}
	b, err := json.Marshal(dbResp)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// UserLoginHandler returns a session token for a user
func (u User) UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	email, password, ok := r.BasicAuth()
	if ok {
		usernameHash := sha256.Sum256([]byte(email))

		// fetch email & pass from db
		dbEmailResp, err := u.DB.Find(context.Background(), bson.M{"user.email": email})
		if err != nil {
			config.ErrorStatus("failed to get user by ID", http.StatusNotFound, w, err)
			return
		}
		if len(dbEmailResp) == 0 {
			config.ErrorStatus("no matching email found", http.StatusUnauthorized, w, fmt.Errorf("no matching email found"))
			return
		}

		expectedUsernameHash := sha256.Sum256([]byte(dbEmailResp[0].Details.Email))
		usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1

		err = bcrypt.CompareHashAndPassword([]byte(dbEmailResp[0].Details.Password), []byte(password))
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
	existingUser, _ := u.DB.FindOne(context.Background(), bson.M{"user.email": user.Email})
	if existingUser != nil {
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

	// insert the user
	_ = u.DB.InsertOne(context.Background(), user)
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
	existingUser, _ := u.DB.FindOne(context.Background(), bson.M{"user.email": user.Email})
	if existingUser != nil {
		config.ErrorStatus("email already exists", http.StatusConflict, w, fmt.Errorf("duplicate email"))
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UsersDiscoverPeopleHandler returns a list of users that we suggest to the user to follow
func (u User) UsersDiscoverPeopleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	email := r.URL.Query().Get("email")
	if email == "" {
		config.ErrorStatus("query param email is required", http.StatusBadRequest, w, fmt.Errorf("query param email is required"))
		return
	}

	pipeline := []bson.M{
		{"$match": bson.M{"user.email": bson.M{"$ne": email}}},
		{"$sample": bson.M{"size": 4}},
	}

	cursor, err := u.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to get discover people recommendations", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	for cursor.Next(context.Background()) {
		if err := cursor.Decode(&users); err != nil {
			config.ErrorStatus("failed to decode user", http.StatusInternalServerError, w, err)
			return
		}
	}

	if err := cursor.Err(); err != nil {
		config.ErrorStatus("cursor error", http.StatusInternalServerError, w, err)
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
	user, err := u.DB.FindOne(context.Background(), bson.M{"_id": uID})
	if err != nil {
		config.ErrorStatus("failed to get user by userId", http.StatusNotFound, w, err)
		return
	}

	// Get the last accessed community
	lastAccessedCommunity := user.Details.LastAccessedCommunity
	if lastAccessedCommunity == (models.LastAccessedCommunity{}) {
		config.ErrorStatus("no last accessed community found", http.StatusNotFound, w, fmt.Errorf("no last accessed community found"))
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

	// Calculate the time difference
	// now := time.Now()
	// var lastAccessedTime time.Time
	// switch v := lastAccessedCommunity.CreatedAt.(type) {
	// case time.Time:
	// 	lastAccessedTime = v
	// case primitive.DateTime:
	// 	lastAccessedTime = v.Time()
	// default:
	// 	config.ErrorStatus("invalid last accessed time", http.StatusInternalServerError, w, fmt.Errorf("invalid last accessed time"))
	// 	return
	// }
	// duration := now.Sub(lastAccessedTime)

	// var lastAccessed string
	// hours := duration.Hours()
	// if hours <= 24 {
	// 	lastAccessed = fmt.Sprintf("%.0f hours", hours)
	// } else if hours <= 24*365 {
	// 	days := hours / 24
	// 	lastAccessed = fmt.Sprintf("%.0f days", days)
	// } else {
	// 	years := hours / (24 * 365)
	// 	lastAccessed = fmt.Sprintf("%.0f years", years)
	// }

	// community.Details.LastAccessed = lastAccessed

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
	email := r.URL.Query().Get("email")
	if email == "" {
		config.ErrorStatus("query param email is required", http.StatusBadRequest, w, fmt.Errorf("query param email is required"))
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

	zap.S().Debugf("email: %v, limit: %v, page: %v", email, limit, page)
	filter := bson.M{"user.email": email}

	dbResp, err := u.DB.FindOne(context.Background(), filter)
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
			continue // Skip invalid ObjectID
		}
		friendDetails, err := u.DB.FindOne(context.Background(), bson.M{"_id": fID})
		if err != nil {
			continue // Skip if friend not found
		}

		detailedFriend := map[string]interface{}{
			"friend_id":  friend.FriendID,
			"status":     friend.Status,
			"created_at": friend.CreatedAt,
			"avatar":     friendDetails.Details.ProfilePicture,
			"user_name":  friendDetails.Details.Username,
			"userName":   friendDetails.Details.Username,
			"name":       friendDetails.Details.Name,
			"createdAt":  friend.CreatedAt,
			"numFriends": len(friendDetails.Details.Friends),
			"isOnline":   friend.IsOnline,
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
	email := mux.Vars(r)["email"]
	if email == "" {
		config.ErrorStatus("query param email is required", http.StatusBadRequest, w, fmt.Errorf("query param email is required"))
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

	// Check if the friend already exists
	filter := bson.M{"user.email": email, "user.friends.friend_id": friend.FriendID}
	existingFriend, err := u.DB.FindOne(context.Background(), filter)
	if err == nil && existingFriend != nil {
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

	filter = bson.M{"user.email": email}
	update := bson.M{"$push": bson.M{"user.friends": newFriend}}
	opts := options.Update().SetUpsert(false)

	_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
	if err != nil {
		config.ErrorStatus("failed to add friend", http.StatusInternalServerError, w, err)
		return
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
		dbResp, err := u.DB.FindOne(context.Background(), filter)
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
		Seen:       false,
		CreatedAt:  time.Now(),
	}

	nID, err := primitive.ObjectIDFromHex(notification.SentToID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": nID}
	update := bson.M{"$push": bson.M{"user.notifications": newNotification}}
	opts := options.Update().SetUpsert(false)

	_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
	if err != nil {
		config.ErrorStatus("failed to create notification", http.StatusInternalServerError, w, err)
		return
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
	dbResp, err := u.DB.FindOne(context.Background(), filter)
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

		sender, err := u.DB.FindOne(context.Background(), bson.M{"_id": senderID})
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
	dbResp, err := u.DB.FindOne(context.Background(), filter)
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
	friendResp, err := u.DB.FindOne(context.Background(), friendFilter)
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
	dbResp, err := u.DB.FindOne(context.Background(), filter)
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
	dbResp, err := u.DB.FindOne(context.Background(), filter)
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
	dbResp, err := u.DB.FindOne(context.Background(), filter)
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
	friendResp, err := u.DB.FindOne(context.Background(), friendFilter)
	if err != nil {
		config.ErrorStatus("failed to get friend by ID", http.StatusNotFound, w, err)
		return
	}

	userFilter := bson.M{"_id": uID}
	userResp, err := u.DB.FindOne(context.Background(), userFilter)
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

	// Check if the user and friend are mutual friends
	userIsFriendOfFriend := false
	friendIsFriendOfUser := false

	for _, friend := range approvedFriendFriends {
		if friend.FriendID == userID {
			userIsFriendOfFriend = true
			break
		}
	}

	for _, friend := range userFriends {
		if friend.FriendID == friendID && friend.Status == "approved" {
			friendIsFriendOfUser = true
			break
		}
	}

	if userIsFriendOfFriend && friendIsFriendOfUser {
		mutualFriendsCount++
	}

	response := map[string]interface{}{
		"friendCount":        len(approvedFriendFriends),
		"mutualFriendsCount": mutualFriendsCount,
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

	// Find the user by ID
	user, err := u.DB.FindOne(context.Background(), bson.M{"_id": uID})
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

	// Find the user by ID
	user, err := u.DB.FindOne(context.Background(), bson.M{"_id": uID})
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
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit)).SetSort(bson.M{"$natural": 1})

	cursor, err := u.CDB.Find(context.Background(), filter, options)
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

	// Parse the request body to get the community ID
	var requestBody struct {
		CommunityID string `json:"communityId"`
		Status      string `json:"status"`
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

	// Find the pending community request
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

	// Update the status to "approved"
	_, err = u.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update community status", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community request approved successfully"}`))
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

	// Check if the communityId already exists in the user's pending community requests
	existingUser, err := u.DB.FindOne(context.Background(), bson.M{
		"_id": uID,
		"user.communities": bson.M{
			"$elemMatch": bson.M{
				"communityId": requestBody.CommunityID,
			},
		},
	})
	if err == nil && existingUser != nil {
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
