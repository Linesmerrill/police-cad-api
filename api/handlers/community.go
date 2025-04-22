package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Community struct mostly used for mocking tests
type Community struct {
	DB  databases.CommunityDatabase
	UDB databases.UserDatabase
}

// CommunityHandler returns a community given a communityID
func (c Community) CommunityHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]

	// Retrieve optional query parameters
	field := r.URL.Query().Get("field")
	value := r.URL.Query().Get("value")

	zap.S().Debugf("community_id: %v, field: %v, value: %v", commID, field, value)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Base filter
	filter := bson.M{"_id": cID}

	// Add dynamic filter if field and value are provided
	if field != "" && value != "" {
		filter["community."+field] = value
	}

	dbResp, err := c.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
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

// CreateCommunityHandler creates a new community
func (c Community) CreateCommunityHandler(w http.ResponseWriter, r *http.Request) {
	var newCommunity models.Community

	// Parse the request body to get the new community details
	if err := json.NewDecoder(r.Body).Decode(&newCommunity); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the community
	newCommunity.ID = primitive.NewObjectID()
	// Set the createdAt and updatedAt fields to the current time
	newCommunity.Details.CreatedAt = primitive.NewDateTimeFromTime(time.Now())
	newCommunity.Details.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())
	newCommunity.Details.InviteCodes = []models.InviteCode{}
	newCommunity.Details.BanList = []string{}
	newCommunity.Details.Departments = []models.Department{}
	newCommunity.Details.TenCodes = defaultTenCodes()
	newCommunity.Details.Fines = defaultCommunityFines()
	newCommunity.Details.MembersCount = 1

	// Initialize the events slice if it is null
	if newCommunity.Details.Events == nil {
		newCommunity.Details.Events = []models.Event{}
	}

	// Define the Head Admin role and permission
	headAdminRole := models.Role{
		ID:      primitive.NewObjectID(),
		Name:    "Head Admin",
		Members: []string{newCommunity.Details.OwnerID},
		Permissions: []models.Permission{
			{
				ID:          primitive.NewObjectID(),
				Name:        "administrator",
				Description: "Head Admin",
				Enabled:     true,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage community settings",
				Description: "Allows managing community settings",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage community events",
				Description: "Allows managing community events",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage departments",
				Description: "Allows managing departments",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage roles",
				Description: "Allows managing roles",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage members",
				Description: "Allows managing members",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "manage bans",
				Description: "Allows managing bans",
				Enabled:     false,
			},
			{
				ID:          primitive.NewObjectID(),
				Name:        "administrator",
				Description: "Members with this permission will have every permission and will also bypass all community specific permissions or restrictions (for example, these members would get access to all settings and pages). This is a dangerous permission to grant.",
				Enabled:     false,
			},
		},
	}

	// Add the Head Admin role to the community
	newCommunity.Details.Roles = append(newCommunity.Details.Roles, headAdminRole)

	// Insert the new community into the database
	_, err := c.DB.InsertOne(context.Background(), newCommunity)
	if err != nil {
		config.ErrorStatus("failed to create community", http.StatusInternalServerError, w, err)
		return
	}

	// Add the community to the user's communities array
	ownerID := newCommunity.Details.OwnerID
	uID, err := primitive.ObjectIDFromHex(ownerID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Create a new UserCommunity object
	newUserCommunity := models.UserCommunity{
		ID:          primitive.NewObjectID().Hex(),
		CommunityID: newCommunity.ID.Hex(),
		Status:      "approved",
	}

	// Ensure the user's communities array is initialized
	filter := bson.M{"_id": uID}

	user := models.User{}
	err = c.UDB.FindOne(context.Background(), filter).Decode(&user)
	if err != nil {
		config.ErrorStatus("failed to retrieve user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Initialize null fields
	if user.Details.Communities == nil {
		user.Details.Communities = []models.UserCommunity{}
	}
	if user.Details.Friends == nil {
		user.Details.Friends = []models.Friend{}
	}
	if user.Details.Notifications == nil {
		user.Details.Notifications = []models.Notification{}
	}

	// Update the user's communities array
	update := bson.M{
		"$set": bson.M{"user.communities": user.Details.Communities}, // Ensure communities is an array
	}
	_, err = c.UDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Add the new community to the user's communities array
	update = bson.M{
		"$addToSet": bson.M{"user.communities": newUserCommunity}, // $addToSet ensures no duplicates
	}
	_, err = c.UDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update user's communities", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":   "Community created successfully",
		"community": newUserCommunity,
	})
}

// CommunityByCommunityAndOwnerIDHandler returns a community that contains the specified ownerID
func (c Community) CommunityByCommunityAndOwnerIDHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]
	ownerID := mux.Vars(r)["owner_id"]

	zap.S().Debugf("community_id: %v, owner_id: %v", commID, ownerID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dbResp, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID, "community.ownerID": ownerID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID and ownerID", http.StatusNotFound, w, err)
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

// CommunitiesByOwnerIDHandler returns all communities that contain the specified ownerID
func (c Community) CommunitiesByOwnerIDHandler(w http.ResponseWriter, r *http.Request) {
	ownerID := mux.Vars(r)["owner_id"]

	zap.S().Debugf("owner_id: %v", ownerID)

	// Parse query parameters for pagination
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}

	// Calculate the offset for pagination
	offset := (page - 1) * limit

	// Find communities by owner ID with pagination
	filter := bson.M{"community.ownerID": ownerID}
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit))

	cursor, err := c.DB.Find(context.Background(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get communities by ownerID", http.StatusNotFound, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var communities []models.Community
	if err = cursor.All(context.Background(), &communities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	b, err := json.Marshal(communities)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CommunityMembersHandler returns all members of a community
func (c Community) CommunityMembersHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse query parameters for pagination
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}

	// Calculate the offset for pagination
	offset := (page - 1) * limit

	// Find all users that belong to the community with pagination
	filter := bson.M{
		"$and": []bson.M{
			{"user.communities": bson.M{"$exists": true}},
			{"user.communities": bson.M{"$ne": nil}},
			{"user.communities": bson.M{
				"$elemMatch": bson.M{
					"communityId": communityID,
					"status":      "approved",
				},
			}},
		},
	}

	// Count the total number of users
	totalUsers, err := c.UDB.CountDocuments(context.Background(), filter)
	if err != nil {
		return
	}

	// Fetch only the first 10 users' details
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit))
	cursor, err := c.UDB.Find(context.Background(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get users by community ID", http.StatusInternalServerError, w, err)
		return
	}

	defer cursor.Close(context.Background())

	var users []models.User
	if err = cursor.All(context.Background(), &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}
	var members []models.User
	onlineCount := 0

	for _, user := range users {
		members = append(members, user)
		if user.Details.IsOnline {
			onlineCount++
		}
	}

	response := map[string]interface{}{
		"members":     members,
		"onlineCount": onlineCount,
		"totalUsers":  totalUsers,
		"page":        page,
		"limit":       limit,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// GetEventsByCommunityIDHandler returns all events of a community
func (c Community) GetEventsByCommunityIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse query parameters for pagination
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}

	// Calculate the offset for pagination
	offset := (page - 1) * limit

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	comm, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Extract the events from the community
	events := comm.Details.Events

	// Apply pagination to the events
	start := offset
	end := offset + limit
	if start > len(events) {
		start = len(events)
	}
	if end > len(events) {
		end = len(events)
	}
	paginatedEvents := events[start:end]

	// Marshal the paginated events to JSON
	b, err := json.Marshal(paginatedEvents)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// AddEventToCommunityHandler adds an event to a community
func (c Community) AddEventToCommunityHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body to get the event details
	var event models.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the event
	event.ID = primitive.NewObjectID()

	// Set the createdAt and updatedAt fields to the current time
	now := primitive.NewDateTimeFromTime(time.Now())
	event.CreatedAt = now
	event.UpdatedAt = now

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Initialize the events slice if it is null
	if community.Details.Events == nil {
		community.Details.Events = []models.Event{}
	}

	// Update the community to add the new event
	filter := bson.M{"_id": cID}
	update := bson.M{"$push": bson.M{"community.events": event}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add event to community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Event added successfully"}`))
}

// GetEventByIDHandler returns an event by ID
func (c Community) GetEventByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	eventID := mux.Vars(r)["eventId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Convert the event ID to a primitive.ObjectID
	eID, err := primitive.ObjectIDFromHex(eventID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	comm, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Find the event by ID within the community
	var event *models.Event
	for _, evt := range comm.Details.Events {
		if evt.ID == eID {
			event = &evt
			break
		}
	}

	if event == nil {
		config.ErrorStatus("event not found", http.StatusNotFound, w, nil)
		return
	}

	// Marshal the event to JSON
	b, err := json.Marshal(event)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// UpdateEventByIDHandler updates an event by ID
func (c Community) UpdateEventByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	eventID := mux.Vars(r)["eventId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Convert the event ID to a primitive.ObjectID
	eID, err := primitive.ObjectIDFromHex(eventID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Parse the request body to get the updated event details
	var updatedEvent map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedEvent); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Set the updatedAt field to the current time
	updatedEvent["updatedAt"] = primitive.NewDateTimeFromTime(time.Now())

	// Create the update document dynamically
	update := bson.M{}
	for key, value := range updatedEvent {
		update["community.events.$."+key] = value
	}

	// Update the event in the community
	filter := bson.M{"_id": cID, "community.events._id": eID}
	err = c.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update event in community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Event updated successfully"}`))
}

// DeleteEventByIDHandler deletes an event by ID
func (c Community) DeleteEventByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	eventID := mux.Vars(r)["eventId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Convert the event ID to a primitive.ObjectID
	eID, err := primitive.ObjectIDFromHex(eventID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the community to pull the event by ID
	filter := bson.M{"_id": cID}
	update := bson.M{"$pull": bson.M{"community.events": bson.M{"_id": eID}}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete event from community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Event deleted successfully"}`))
}

// UpdateCommunityFieldHandler updates a field in a community
func (c Community) UpdateCommunityFieldHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["community_id"]

	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("invalid request body", http.StatusBadRequest, w, err)
		return
	}

	objID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	// Prefix the keys with "community." to update nested fields
	update := bson.M{}
	for key, value := range req {
		update["community."+key] = value
	}

	err = c.DB.UpdateOne(context.Background(), bson.M{"_id": objID}, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "community updated successfully"}`))
}

// GetRolesByCommunityIDHandler fetches all roles for a given community ID
func (c Community) GetRolesByCommunityIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	var community *models.Community
	community, err = c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Marshal the roles to JSON
	b, err := json.Marshal(community.Details.Roles)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// AddRoleToCommunityHandler adds a role to a community
func (c Community) AddRoleToCommunityHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body to get the role details
	var role models.Role
	if err := json.NewDecoder(r.Body).Decode(&role); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the role
	role.ID = primitive.NewObjectID()

	// Initialize the Members field as an empty array
	role.Members = []string{}

	var DefaultPermissions = []models.Permission{
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage community settings",
			Description: "Allows managing community settings",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage community events",
			Description: "Allows managing community events",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage departments",
			Description: "Allows managing departments",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage roles",
			Description: "Allows managing roles",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage members",
			Description: "Allows managing members",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "manage bans",
			Description: "Allows managing bans",
			Enabled:     false,
		},
		{
			ID:          primitive.NewObjectID(),
			Name:        "administrator",
			Description: "Members with this permission will have every permission and will also bypass all community specific permissions or restrictions (for example, these members would get access to all settings and pages). This is a dangerous permission to grant.",
			Enabled:     false,
		},
	}

	// Add default permissions to the role
	role.Permissions = append(role.Permissions, DefaultPermissions...)

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the community to add the new role
	filter := bson.M{"_id": cID}
	update := bson.M{"$push": bson.M{"community.roles": role}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add role to community", http.StatusInternalServerError, w, err)
		return
	}

	response := map[string]interface{}{
		"message": "Role added successfully",
		"role_id": role.ID.Hex(),
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// UpdateRoleMembersHandler updates the members of a role in a community
func (c Community) UpdateRoleMembersHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Parse the request body to get the new member IDs
	var memberIDs []string
	if err := json.NewDecoder(r.Body).Decode(&memberIDs); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Append new member IDs to the array
	filter := bson.M{"_id": cID, "community.roles._id": rID}
	update := bson.M{
		"$addToSet": bson.M{
			"community.roles.$.members": bson.M{
				"$each": memberIDs,
			},
		},
	}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update role members", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role members updated successfully"}`))
}

// UpdateRoleNameHandler updates the name of a role in a community
func (c Community) UpdateRoleNameHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Parse the request body to get the new name
	var requestBody struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the role name in the community
	filter := bson.M{"_id": cID, "community.roles._id": rID}
	update := bson.M{"$set": bson.M{"community.roles.$.name": requestBody.Name}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update role name", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role name updated successfully"}`))
}

// DeleteRoleByIDHandler deletes a role by communityId and roleId
func (c Community) DeleteRoleByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the community to pull the role by ID
	filter := bson.M{"_id": cID}
	update := bson.M{"$pull": bson.M{"community.roles": bson.M{"_id": rID}}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete role from community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role deleted successfully"}`))
}

// UpdateRolePermissionsHandler updates the permissions of a role in a community
func (c Community) UpdateRolePermissionsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Parse the request body to get the new permissions
	var requestBody struct {
		Permissions []models.Permission `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the role permissions in the community
	filter := bson.M{"_id": cID, "community.roles._id": rID}
	update := bson.M{"$set": bson.M{"community.roles.$.permissions": requestBody.Permissions}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update role permissions", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role permissions updated successfully"}`))
}

// DeleteCommunityByIDHandler deletes a community by ID and removes references from all users
func (c Community) DeleteCommunityByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["community_id"]

	// Convert the community ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Delete the community by ID
	communityFilter := bson.M{"_id": cID}
	err = c.DB.DeleteOne(context.Background(), communityFilter)
	if err != nil {
		config.ErrorStatus("failed to delete community", http.StatusInternalServerError, w, err)
		return
	}

	// Remove the community references from all users
	userFilter := bson.M{"user.communities.communityId": communityID}
	userUpdate := bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": communityID}}}
	_, err = c.UDB.UpdateMany(context.Background(), userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove community references from users", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community deleted and references removed successfully"}`))
}

// GetBannedUsersHandler returns all banned users of a community
func (c Community) GetBannedUsersHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Get the list of banned user IDs
	banList := community.Details.BanList

	// Convert banList to a slice of primitive.ObjectID
	var objectIDs []primitive.ObjectID
	for _, id := range banList {
		objID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			config.ErrorStatus("failed to convert banList ID to ObjectID", http.StatusInternalServerError, w, err)
			return
		}
		objectIDs = append(objectIDs, objID)
	}

	if len(objectIDs) == 0 {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
		return
	}

	// Find the banned users
	userFilter := bson.M{"_id": bson.M{"$in": objectIDs}}
	cursor, err := c.UDB.Find(context.Background(), userFilter)
	if err != nil {
		config.ErrorStatus("failed to get banned users", http.StatusInternalServerError, w, err)
		return
	}

	defer cursor.Close(context.Background())

	var bannedUsers []models.User
	if err = cursor.All(context.Background(), &bannedUsers); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	// If no banned users are found, return an empty array
	if bannedUsers == nil {
		bannedUsers = []models.User{}
	}

	// Marshal the banned users to JSON
	b, err := json.Marshal(bannedUsers)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// UnbanUserFromCommunityHandler unbans a user from a community
func (u User) UnbanUserFromCommunityHandler(w http.ResponseWriter, r *http.Request) {
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

	// Delete the matching community object from the user's array of communities
	userFilter := bson.M{"_id": uID}
	userUpdate := bson.M{"$pull": bson.M{"user.communities": bson.M{"communityId": requestBody.CommunityID}}}
	_, err = u.DB.UpdateOne(context.Background(), userFilter, userUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove community from user's communities", http.StatusInternalServerError, w, err)
		return
	}

	// Remove the user ID from the community's banList
	communityFilter := bson.M{"_id": cID}
	communityUpdate := bson.M{"$pull": bson.M{"community.banList": userID}}
	err = u.CDB.UpdateOne(context.Background(), communityFilter, communityUpdate)
	if err != nil {
		config.ErrorStatus("failed to remove user from community ban list", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User unbanned from community successfully"}`))
}

// AddInviteCodeHandler adds a new invite code to the community's inviteCodes array
func (c Community) AddInviteCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body to get the invite code details
	var newInviteCode models.InviteCode
	if err := json.NewDecoder(r.Body).Decode(&newInviteCode); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Set the CreatedAt field to the current time
	newInviteCode.CreatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Update the community's inviteCodes array
	filter := bson.M{"_id": cID}
	update := bson.M{"$addToSet": bson.M{"community.inviteCodes": newInviteCode}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add invite code to community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Invite code added successfully"}`))
}

// DeleteRoleMemberHandler deletes a member from a role in a community
func (c Community) DeleteRoleMemberHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]
	memberID := mux.Vars(r)["memberId"]

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the community to pull the member from the role
	filter := bson.M{"_id": cID, "community.roles._id": rID}
	update := bson.M{"$pull": bson.M{"community.roles.$.members": memberID}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete role member", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role member deleted successfully"}`))
}

// FetchCommunityMembersByRoleIDHandler returns all members of a role in a community
func (c Community) FetchCommunityMembersByRoleIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Convert the community ID and role ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	rID, err := primitive.ObjectIDFromHex(roleID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Find the role by ID within the community
	var role *models.Role
	for _, r := range community.Details.Roles {
		if r.ID == rID {
			role = &r
			break
		}
	}

	if role == nil {
		config.ErrorStatus("role not found", http.StatusNotFound, w, nil)
		return
	}

	// Return the members array
	response := map[string]interface{}{
		"members": role.Members,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// FetchUserDepartmentsHandler returns all departments where the user is a member with status "approved"
func (c Community) FetchUserDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	zap.S().Debugf("Fetch user departments")
	communityID := mux.Vars(r)["communityId"]
	userID := mux.Vars(r)["userId"]

	// Convert the community ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Initialize departments to an empty array if it is null
	if community.Details.Departments == nil {
		community.Details.Departments = []models.Department{}
	}

	// Initialize the userDepartments slice
	var userDepartments []models.Department

	// Filter departments where the user is a member with status "approved" or approval is not required
	for _, department := range community.Details.Departments {
		zap.S().Debugf("Fetch department by department name %s", department.Name)
		if !department.ApprovalRequired {
			userDepartments = append(userDepartments, department)
		} else {
			for _, member := range department.Members {
				if member.UserID == userID && member.Status == "approved" {
					userDepartments = append(userDepartments, department)
					break
				}
			}
		}
	}

	// Return the filtered departments
	response := map[string]interface{}{
		"departments": userDepartments,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// FetchAllCommunityDepartmentsHandler returns all departments of a community
func (c Community) FetchAllCommunityDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Convert the community ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Initialize departments to an empty array if it is null
	if community.Details.Departments == nil {
		community.Details.Departments = []models.Department{}
	}

	// Return the departments array
	response := map[string]interface{}{
		"departments": community.Details.Departments,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// CreateCommunityDepartmentHandler adds a new department to a community
func (c Community) CreateCommunityDepartmentHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body to get the department details
	var department models.Department
	if err := json.NewDecoder(r.Body).Decode(&department); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Generate a new _id for the department if not supplied
	if department.ID.IsZero() {
		department.ID = primitive.NewObjectID()
	}

	// Generate new _id for the template if not supplied
	if department.Template.ID.IsZero() {
		department.Template.ID = primitive.NewObjectID()
	}

	// Generate new _id for each component if not supplied
	for i := range department.Template.Components {
		if department.Template.Components[i].ID.IsZero() {
			department.Template.Components[i].ID = primitive.NewObjectID()
		}
	}

	// Set the createdAt and updatedAt fields to the current time
	now := primitive.NewDateTimeFromTime(time.Now())
	department.CreatedAt = now
	department.UpdatedAt = now

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Initialize the departments slice if it is null
	if community.Details.Departments == nil {
		community.Details.Departments = []models.Department{}
	}

	// Update the community to add the new department
	filter := bson.M{"_id": cID}
	update := bson.M{"$push": bson.M{"community.departments": department}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add department to community", http.StatusInternalServerError, w, err)
		return
	}

	response := map[string]interface{}{
		"message":       "Department added successfully",
		"department_id": department.ID.Hex(),
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// DeleteCommunityDepartmentByIDHandler deletes a department by ID
func (c Community) DeleteCommunityDepartmentByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Convert the community ID and department ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Update the community to pull the department by ID
	filter := bson.M{"_id": cID}
	update := bson.M{"$pull": bson.M{"community.departments": bson.M{"_id": dID}}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete department from community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Department deleted successfully"}`))
}

// UpdateDepartmentMembersHandler updates the members of a department in a community
func (c Community) UpdateDepartmentMembersHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var requestBody struct {
		Members []string `json:"members"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	for _, memberID := range requestBody.Members {
		mID := primitive.NewObjectID()
		filter := bson.M{
			"_id":                                  cID,
			"community.departments._id":            dID,
			"community.departments.members.userID": bson.M{"$ne": memberID},
		}
		update := bson.M{
			"$addToSet": bson.M{
				"community.departments.$.members": bson.M{
					"_id":    mID,
					"status": "approved",
					"userID": memberID,
				},
			},
		}
		err = c.DB.UpdateOne(context.Background(), filter, update)
		if err != nil {
			config.ErrorStatus("failed to update department members", http.StatusInternalServerError, w, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Department members updated successfully"}`))
}

// SetMemberTenCodeHandler sets the Ten-Code for a member in a department
func (c Community) SetMemberTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	userID := mux.Vars(r)["userId"]

	var requestBody models.MemberDetail
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Ensure the members map is initialized
	if community.Details.Members == nil {
		community.Details.Members = make(map[string]models.MemberDetail)
	}

	// Update or add the TenCodeID for the user
	members := community.Details.Members
	members[userID] = models.MemberDetail{
		DepartmentID: requestBody.DepartmentID,
		TenCodeID:    requestBody.TenCodeID,
	}

	// Update the community in the database
	filter := bson.M{"_id": cID}
	update := bson.M{"$set": bson.M{"community.members": members}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update member Ten-Code", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Ten-Code set successfully"}`))
}

// FetchDepartmentByIDHandler returns a department by ID
func (c Community) FetchDepartmentByIDHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Convert the community ID and department ID to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to get community by ID", http.StatusNotFound, w, err)
		return
	}

	// Find the department by ID within the community
	var department *models.Department
	for _, dept := range community.Details.Departments {
		if dept.ID == dID {
			department = &dept
			break
		}
	}

	if department == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, nil)
		return
	}

	// Return the department details
	response := map[string]interface{}{
		"department": department,
	}

	b, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

// RemoveUserFromDepartmentHandler removes a user from a department
func (c Community) RemoveUserFromDepartmentHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var requestBody struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": cID, "community.departments._id": dID}
	update := bson.M{"$pull": bson.M{"community.departments.$.members": bson.M{"userID": requestBody.UserID}}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to remove user from department", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "User removed from department successfully"}`))
}

// UpdateDepartmentImageLinkHandler updates the image link of a department
func (c Community) UpdateDepartmentImageLinkHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var requestBody struct {
		ImageURL string `json:"imageUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": cID, "community.departments._id": dID}
	update := bson.M{"$set": bson.M{"community.departments.$.image": requestBody.ImageURL}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update department image link", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Department image link updated successfully"}`))
}

// UpdateDepartmentDetailsHandler updates the details of a department
func (c Community) UpdateDepartmentDetailsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	var updatedDetails map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updatedDetails); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	update := bson.M{}
	for key, value := range updatedDetails {
		update["community.departments.$."+key] = value
	}

	filter := bson.M{"_id": cID, "community.departments._id": dID}
	err = c.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update})
	if err != nil {
		config.ErrorStatus("failed to update department details", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Department details updated successfully"}`))
}

// UpdateDepartmentJoinRequestHandler updates the join request status for a user in a department
func (c Community) UpdateDepartmentJoinRequestHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	departmentID := vars["departmentId"]

	var requestBody struct {
		UserID string `json:"userId"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert IDs to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid communityId", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid departmentId", http.StatusBadRequest, w, err)
		return
	}

	// Find the community by ID
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
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

	// Update the user's join request status in the department
	updated := false
	for i, member := range department.Members {
		if member.UserID == requestBody.UserID {
			department.Members[i].Status = requestBody.Status
			updated = true
			break
		}
	}

	if !updated {
		config.ErrorStatus("user not found in department members", http.StatusNotFound, w, nil)
		return
	}

	// Update the community in the database
	update := bson.M{"$set": bson.M{"community.departments": community.Details.Departments}}
	err = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, update)
	if err != nil {
		config.ErrorStatus("failed to update community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Department join request updated successfully"}`))
}

// DeleteTenCodeHandler deletes a Ten-Code from a department
func (c Community) DeleteTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	codeID := mux.Vars(r)["codeId"]

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	tID, err := primitive.ObjectIDFromHex(codeID)
	if err != nil {
		config.ErrorStatus("invalid Ten-Code ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{
		"_id": cID,
	}
	update := bson.M{
		"$pull": bson.M{"community.tenCodes": bson.M{"_id": tID}},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to delete Ten-Code", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Ten-Code deleted successfully"}`))
}

// UpdateTenCodeHandler updates a Ten-Code in a department
func (c Community) UpdateTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	codeID := mux.Vars(r)["codeId"]

	var requestBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	tID, err := primitive.ObjectIDFromHex(codeID)
	if err != nil {
		config.ErrorStatus("invalid Ten-Code ID", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{
		"_id":                    cID,
		"community.tenCodes._id": tID,
	}

	update := bson.M{}
	for key, value := range requestBody {
		update["community.tenCodes.$[tenCode]."+key] = value
	}

	arrayFilters := options.Update().SetArrayFilters(options.ArrayFilters{
		Filters: []interface{}{bson.M{"tenCode._id": tID}},
	})

	err = c.DB.UpdateOne(context.Background(), filter, bson.M{"$set": update}, arrayFilters)
	if err != nil {
		config.ErrorStatus("failed to update Ten-Code", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Ten-Code updated successfully"}`))
}

// AddTenCodeHandler adds a new Ten-Code to a department
func (c Community) AddTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	var requestBody struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		Category    string `json:"category"` // Example of a new field in the updated model
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Create a new Ten-Code object based on the updated model
	newTenCode := models.TenCodes{
		ID:          primitive.NewObjectID(),
		Code:        requestBody.Code,
		Description: requestBody.Description,
	}

	filter := bson.M{
		"_id": cID,
	}
	update := bson.M{
		"$push": bson.M{"community.tenCodes": newTenCode},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add Ten-Code", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Ten-Code added successfully",
		"tenCode": newTenCode,
	})
}

func defaultTenCodes() []models.TenCodes {
	return []models.TenCodes{
		{ID: primitive.NewObjectID(), Code: "Signal 100", Description: "HOLD ALL BUT EMERGENCY"},
		{ID: primitive.NewObjectID(), Code: "Signal 60", Description: "Drugs"},
		{ID: primitive.NewObjectID(), Code: "Signal 41", Description: "Log on to MDT"},
		{ID: primitive.NewObjectID(), Code: "Signal 42", Description: "Log out of MDT"},
		{ID: primitive.NewObjectID(), Code: "Signal 37", Description: "Meet @ ..."},
		{ID: primitive.NewObjectID(), Code: "Code 4", Description: "Under Control"},
		{ID: primitive.NewObjectID(), Code: "Code 5", Description: "Felony Stop/High Risk Stop"},
		{ID: primitive.NewObjectID(), Code: "10-0", Description: "Disappeared"},
		{ID: primitive.NewObjectID(), Code: "10-1", Description: "Frequency Change"},
		{ID: primitive.NewObjectID(), Code: "10-2", Description: "Radio Check Loud and Clear"},
		{ID: primitive.NewObjectID(), Code: "10-3", Description: "Stop Transmitting"},
		{ID: primitive.NewObjectID(), Code: "10-4", Description: "Affirmative"},
		{ID: primitive.NewObjectID(), Code: "10-6", Description: "Busy"},
		{ID: primitive.NewObjectID(), Code: "10-7", Description: "Out of Service"},
		{ID: primitive.NewObjectID(), Code: "10-8", Description: "In Service"},
		{ID: primitive.NewObjectID(), Code: "10-9", Description: "Repeat Last Transmission"},
		{ID: primitive.NewObjectID(), Code: "10-10", Description: "Fight in Progress"},
		{ID: primitive.NewObjectID(), Code: "10-11", Description: "Traffic Stop"},
		{ID: primitive.NewObjectID(), Code: "10-12", Description: "Active Ride Along"},
		{ID: primitive.NewObjectID(), Code: "10-13", Description: "Shots Fired"},
		{ID: primitive.NewObjectID(), Code: "10-15", Description: "Subject in Custody"},
		{ID: primitive.NewObjectID(), Code: "10-16", Description: "Stolen Vehicle"},
		{ID: primitive.NewObjectID(), Code: "10-17", Description: "Suspicious Person"},
		{ID: primitive.NewObjectID(), Code: "10-19", Description: "Return to Station"},
		{ID: primitive.NewObjectID(), Code: "10-20", Description: "Location"},
		{ID: primitive.NewObjectID(), Code: "10-21", Description: "Contact via Discord/Text"},
		{ID: primitive.NewObjectID(), Code: "10-22", Description: "Disregard"},
		{ID: primitive.NewObjectID(), Code: "10-23", Description: "Arrived on Scene"},
		{ID: primitive.NewObjectID(), Code: "10-25", Description: "Domestic Dispute"},
		{ID: primitive.NewObjectID(), Code: "10-26", Description: "ETA"},
		{ID: primitive.NewObjectID(), Code: "10-27", Description: "Name Check"},
		{ID: primitive.NewObjectID(), Code: "10-28", Description: "Plate Check"},
		{ID: primitive.NewObjectID(), Code: "10-29", Description: "Warrant Check"},
		{ID: primitive.NewObjectID(), Code: "10-30", Description: "Wanted Person"},
		{ID: primitive.NewObjectID(), Code: "10-31", Description: "Not Wanted, No Warrants"},
		{ID: primitive.NewObjectID(), Code: "10-32", Description: "Request Backup (Code 1-2-3)"},
		{ID: primitive.NewObjectID(), Code: "10-41", Description: "Beginning Tour of Duty"},
		{ID: primitive.NewObjectID(), Code: "10-42", Description: "Ending Tour of Duty"},
		{ID: primitive.NewObjectID(), Code: "10-43", Description: "Information"},
		{ID: primitive.NewObjectID(), Code: "10-49", Description: "Homicide"},
		{ID: primitive.NewObjectID(), Code: "10-50", Description: "Vehicle Accident"},
		{ID: primitive.NewObjectID(), Code: "10-50 PD", Description: "Property Damage Only"},
		{ID: primitive.NewObjectID(), Code: "10-50 PI", Description: "Persons Injured"},
		{ID: primitive.NewObjectID(), Code: "10-50 F", Description: "Fatal"},
		{ID: primitive.NewObjectID(), Code: "10-51", Description: "Request Towing Service"},
		{ID: primitive.NewObjectID(), Code: "10-52", Description: "Request EMS"},
		{ID: primitive.NewObjectID(), Code: "10-53", Description: "Request Fire Department"},
		{ID: primitive.NewObjectID(), Code: "10-55", Description: "Intoxicated Driver"},
		{ID: primitive.NewObjectID(), Code: "10-56", Description: "Suicide"},
		{ID: primitive.NewObjectID(), Code: "10-56A", Description: "Suicide Attempt"},
		{ID: primitive.NewObjectID(), Code: "10-60", Description: "Armed with a Gun"},
		{ID: primitive.NewObjectID(), Code: "10-61", Description: "Armed with a Knife"},
		{ID: primitive.NewObjectID(), Code: "10-62", Description: "Kidnapping"},
		{ID: primitive.NewObjectID(), Code: "10-64", Description: "Sexual Assault"},
		{ID: primitive.NewObjectID(), Code: "10-65", Description: "Escorting Prisoner"},
		{ID: primitive.NewObjectID(), Code: "10-66", Description: "Reckless Driver"},
		{ID: primitive.NewObjectID(), Code: "10-67", Description: "Active Fire"},
		{ID: primitive.NewObjectID(), Code: "10-68", Description: "Armed Robbery"},
		{ID: primitive.NewObjectID(), Code: "10-70", Description: "Foot Pursuit"},
		{ID: primitive.NewObjectID(), Code: "10-71", Description: "Request Supervisor at Scene"},
		{ID: primitive.NewObjectID(), Code: "10-73", Description: "Advise Status or Scene Update"},
		{ID: primitive.NewObjectID(), Code: "10-80", Description: "Vehicle Pursuit"},
		{ID: primitive.NewObjectID(), Code: "10-90", Description: "In Game Warning"},
		{ID: primitive.NewObjectID(), Code: "10-93", Description: "Removed From Game"},
		{ID: primitive.NewObjectID(), Code: "10-97", Description: "In Route"},
		{ID: primitive.NewObjectID(), Code: "10-99", Description: "Officer in Distress Extreme Emergency Only"},
		{ID: primitive.NewObjectID(), Code: "11-44", Description: "Person Deceased"},
	}
}

func defaultCommunityFines() models.CommunityFine {
	return models.CommunityFine{
		Currency: "USD",
		Categories: []models.Category{
			{
				Name: "Traffic Citations",
				Fines: []models.FineDetails{
					{Name: "Speeding", Amount: 150},
					{Name: "Parking Violation", Amount: 50},
					{Name: "Running a Stop Sign", Amount: 100},
					{Name: "Expired Registration", Amount: 75},
				},
			},
			{
				Name: "Misdemeanors",
				Fines: []models.FineDetails{
					{Name: "Public Intoxication", Amount: 200},
					{Name: "Disorderly Conduct", Amount: 250},
					{Name: "Petty Theft", Amount: 300},
				},
			},
			{
				Name: "Felonies",
				Fines: []models.FineDetails{
					{Name: "Assault", Amount: 1000},
					{Name: "Burglary", Amount: 1500},
					{Name: "Drug Possession", Amount: 1200},
				},
			},
			{
				Name: "Other",
				Fines: []models.FineDetails{
					{Name: "Other Violation", Amount: 100},
				},
			},
		},
	}
}

// SetCommunityFinesHandler updates the community fines
func (c Community) SetCommunityFinesHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body
	var finesData struct {
		Currency   string            `json:"currency"`
		Categories []models.Category `json:"categories"`
	}
	if err := json.NewDecoder(r.Body).Decode(&finesData); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate community ID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Prepare the update
	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.fines": models.CommunityFine{
				Currency:   finesData.Currency,
				Categories: finesData.Categories,
			},
		},
	}

	// Update the community in the database
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update community fines", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Community fines updated successfully"}`))
}
