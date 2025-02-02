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
	existingUser, _ := u.DB.FindOne(context.Background(), bson.M{"email": user.Email})
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
	existingUser, _ := u.DB.FindOne(context.Background(), bson.M{"email": user.Email})
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
	email := r.URL.Query().Get("email")
	if email == "" {
		config.ErrorStatus("query param email is required", http.StatusBadRequest, w, fmt.Errorf("query param email is required"))
		return
	}

	// Find the user by email
	user, err := u.DB.FindOne(context.Background(), bson.M{"user.email": email})
	if err != nil {
		config.ErrorStatus("failed to get user by email", http.StatusNotFound, w, err)
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
	now := time.Now()
	var lastAccessedTime time.Time
	switch v := lastAccessedCommunity.CreatedAt.(type) {
	case time.Time:
		lastAccessedTime = v
	case primitive.DateTime:
		lastAccessedTime = v.Time()
	default:
		config.ErrorStatus("invalid last accessed time", http.StatusInternalServerError, w, fmt.Errorf("invalid last accessed time"))
		return
	}
	duration := now.Sub(lastAccessedTime)

	var lastAccessed string
	hours := duration.Hours()
	if hours <= 24 {
		lastAccessed = fmt.Sprintf("%.0f hours", hours)
	} else if hours <= 24*365 {
		days := hours / 24
		lastAccessed = fmt.Sprintf("%.0f days", days)
	} else {
		years := hours / (24 * 365)
		lastAccessed = fmt.Sprintf("%.0f years", years)
	}

	// Create a response with the required details
	response := map[string]interface{}{
		"communityName":      community.Details.Name,
		"communityImageLink": community.Details.ImageLink,
		"lastAccessed":       lastAccessed,
	}

	// Marshal the response
	b, err := json.Marshal(response)
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
	filter := bson.M{"email": email}

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

	b, err := json.Marshal(paginatedFriends)
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
	filter := bson.M{"email": email, "user.friends.friend_id": friend.FriendID}
	existingFriend, err := u.DB.FindOne(context.Background(), filter)
	if err == nil && existingFriend != nil {
		config.ErrorStatus("friend already exists", http.StatusConflict, w, fmt.Errorf("friend already exists"))
		return
	}

	newFriend := models.Friend{
		FriendID:  friend.FriendID,
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	filter = bson.M{"email": email}
	update := bson.M{"$push": bson.M{"user.friends": newFriend}}
	opts := options.Update().SetUpsert(true)

	_, err = u.DB.UpdateOne(context.Background(), filter, update, opts)
	if err != nil {
		config.ErrorStatus("failed to add friend", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "friend added successfully"}`))
}
