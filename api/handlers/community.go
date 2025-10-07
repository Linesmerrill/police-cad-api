package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/subscription"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Community struct mostly used for mocking tests
type Community struct {
	DB   databases.CommunityDatabase
	UDB  databases.UserDatabase
	ADB  databases.ArchivedCommunityDatabase
	IDB  databases.InviteCodeDatabase
	UPDB databases.UserPreferencesDatabase
	CDB  databases.CivilianDatabase
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
	newCommunity.Details.InviteCodeIds = []string{}
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
// Deprecated: Use FetchCommunityMembersHandlerV2 instead
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
// Deprecated: Use FetchBannedUsersHandlerV2 instead
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
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	// Decode the request body directly into InviteCode
	var inviteCode models.InviteCode
	if err := json.NewDecoder(r.Body).Decode(&inviteCode); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert ExpiresAt from *time.Time to *primitive.DateTime if provided (no conversion needed since struct matches)
	var expiresAt *time.Time
	if inviteCode.ExpiresAt != nil {
		expiresAt = inviteCode.ExpiresAt // No conversion needed if JSON provides *time.Time
	}

	inviteCodeObj := primitive.NewObjectID() // Generate a new ObjectID for the invite code
	inviteCodeID := inviteCodeObj.Hex()      // Convert ObjectID to string for storage

	// Create the invite code document
	// Handle infinite use case (maxUses = 0) by setting RemainingUses to -1
	remainingUses := inviteCode.MaxUses
	if inviteCode.MaxUses == 0 {
		remainingUses = -1 // Sentinel value for infinite uses
	}
	inviteCodeDoc := models.InviteCode{
		ID:            inviteCodeObj,
		Code:          inviteCode.Code,
		CommunityID:   communityID, // Store as string
		ExpiresAt:     expiresAt,
		MaxUses:       inviteCode.MaxUses,
		RemainingUses: remainingUses,
		CreatedBy:     inviteCode.CreatedBy,
		CreatedAt:     time.Now(),
	}

	// Insert into inviteCodes collection
	if _, err := c.IDB.InsertOne(context.Background(), inviteCodeDoc); err != nil {
		config.ErrorStatus("Failed to save invite code", http.StatusInternalServerError, w, err)
		return
	}

	// Update the community with the inviteCodeID
	communityObjID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	if err := c.DB.UpdateOne(
		context.Background(),
		bson.M{"_id": communityObjID},
		bson.M{"$push": bson.M{"community.inviteCodeIds": inviteCodeID}},
	); err != nil {
		config.ErrorStatus("Failed to update community", http.StatusInternalServerError, w, err)
		return
	}

	// Respond with minimal data for security
	response := map[string]string{
		"code":        inviteCodeDoc.Code,
		"communityId": inviteCodeDoc.CommunityID,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetInviteCodeHandler validates an invite code and returns community details
func (c Community) GetInviteCodeHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	inviteCode := vars["invite_code"]

	// Query the inviteCodes collection
	// var invite models.InviteCode
	invite, err := c.IDB.FindOne(context.Background(), bson.M{"code": inviteCode})
	if err != nil {
		config.ErrorStatus("Invite code not found", http.StatusNotFound, w, err)
		return
	}

	// Check expiration
	currentTime := time.Now() // 12:40 PM MST, June 07, 2025
	if invite.ExpiresAt != nil && invite.ExpiresAt.Before(currentTime) {
		config.ErrorStatus("Invite code has expired", http.StatusBadRequest, w, nil)
		return
	}

	// Check remaining uses, allow -1 for infinite
	if invite.RemainingUses < -1 {
		config.ErrorStatus("Invite code has no remaining uses", http.StatusBadRequest, w, nil)
		return
	}

	// Fetch community details
	communityObjID, err := primitive.ObjectIDFromHex(invite.CommunityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	// var community models.Community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}

	// Prepare response, exclude expiresAt and remainingUses
	response := map[string]interface{}{
		"communityName": community.Details.Name, // Adjust to community.Community.Name if nested
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// JoinCommunityHandler processes a join request using an invite code
func (c Community) JoinCommunityHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"inviteCode"`
		UserID     string `json:"userId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("Invalid request body", http.StatusBadRequest, w, err)
		return
	}

	// Step 1: Validate the invite code with conditional decrement
	var invite models.InviteCode
	pipeline := bson.A{
		bson.M{
			"$set": bson.M{
				"remainingUses": bson.M{
					"$cond": bson.M{
						"if":   bson.M{"$eq": []interface{}{"$remainingUses", -1}},
						"then": -1,
						"else": bson.M{"$subtract": []interface{}{"$remainingUses", 1}},
					},
				},
			},
		},
	}
	if err := c.IDB.FindOneAndUpdate(
		context.Background(),
		bson.M{"code": req.InviteCode, "remainingUses": bson.M{"$gte": -1}},
		pipeline, // Pass the pipeline array directly
	).Decode(&invite); err != nil {
		if err == mongo.ErrNoDocuments {
			config.ErrorStatus("Invalid or expired invite code", http.StatusBadRequest, w, err)
		} else {
			config.ErrorStatus("Database error", http.StatusInternalServerError, w, err)
		}
		return
	}

	currentTime := time.Now() // 12:42 PM MST, June 07, 2025
	if invite.ExpiresAt != nil && invite.ExpiresAt.Before(currentTime) {
		config.ErrorStatus("Invite code has expired", http.StatusBadRequest, w, nil)
		return
	}

	// Convert IDs to ObjectID
	userObjID, err := primitive.ObjectIDFromHex(req.UserID)
	if err != nil {
		config.ErrorStatus("Invalid user ID", http.StatusBadRequest, w, err)
		return
	}
	communityObjID, err := primitive.ObjectIDFromHex(invite.CommunityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Fetch user and community
	var user models.User
	if err := c.UDB.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user); err != nil {
		config.ErrorStatus("Failed to fetch user", http.StatusInternalServerError, w, err)
		return
	}
	// var community models.Community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": communityObjID})
	if err != nil {
		config.ErrorStatus("Failed to fetch community", http.StatusInternalServerError, w, err)
		return
	}

	// Step 2 & 3: Check existing community status
	var existingCommunity *models.UserCommunity
	for i := range user.Details.Communities {
		if user.Details.Communities[i].CommunityID == invite.CommunityID {
			existingCommunity = &user.Details.Communities[i]
			break
		}
	}

	update := bson.M{}
	if existingCommunity != nil {
		// Step 3: Update status to approved if not approved and not banned, preserve other fields
		if existingCommunity.Status != "approved" && existingCommunity.Status != "banned" {
			update = bson.M{
				"$set": bson.M{
					"user.communities.$": bson.M{
						"_id":         existingCommunity.ID,
						"status":      "approved",
						"communityId": existingCommunity.CommunityID,
					},
				},
			}
			// Match the specific communityId in the query
			_, err = c.UDB.UpdateOne(
				context.Background(),
				bson.M{"_id": userObjID, "user.communities.communityId": invite.CommunityID},
				update,
			)
			if err != nil {
				config.ErrorStatus("Failed to update user status", http.StatusInternalServerError, w, err)
				return
			}
		}
	} else {
		// Step 4: Insert new entry if not banned
		if !contains(community.Details.BanList, req.UserID) {
			newCommunityEntry := bson.M{
				"_id":         primitive.NewObjectID().Hex(),
				"communityId": invite.CommunityID,
				"status":      "approved",
			}

			// Check if user.communities is null and handle accordingly
			if user.Details.Communities == nil {
				// If communities is null, set it to an array with the new entry
				update = bson.M{
					"$set": bson.M{"user.communities": bson.A{newCommunityEntry}},
				}
			} else {
				// If communities already exists as an array, push to it
				update = bson.M{
					"$push": bson.M{"user.communities": newCommunityEntry},
				}
			}

			_, err = c.UDB.UpdateOne(
				context.Background(),
				bson.M{"_id": userObjID},
				update,
			)
			if err != nil {
				config.ErrorStatus("Failed to update user", http.StatusInternalServerError, w, err)
				return
			}
		} else {
			config.ErrorStatus("User is banned from this community", http.StatusForbidden, w, nil)
			return
		}
	}

	// Step 5: Increment membersCount if new entry
	isNewEntry := existingCommunity == nil && !contains(community.Details.BanList, req.UserID)
	if isNewEntry {
		if err := c.DB.UpdateOne(
			context.Background(),
			bson.M{"_id": communityObjID},
			bson.M{"$inc": bson.M{"community.membersCount": 1}},
		); err != nil {
			config.ErrorStatus("Failed to update community members count", http.StatusInternalServerError, w, err)
			return
		}
	}

	// Respond with success
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"status":      "joined",
		"communityId": invite.CommunityID,
		"community": map[string]interface{}{
			"_id":       community.ID.Hex(),
			"name":      community.Details.Name,
			"imageLink": community.Details.ImageLink,
		},
	}
	json.NewEncoder(w).Encode(response)
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// sortDepartmentsByUserPreferences sorts departments based on user's custom order preference
func (c Community) sortDepartmentsByUserPreferences(departments []models.Department, userID, communityID string) []models.Department {
	// If no userID provided, return departments as-is
	if userID == "" {
		return departments
	}

	// Get user preferences
	var userPreferences models.UserPreferences
	err := c.UPDB.FindOne(context.Background(), bson.M{"userId": userID}).Decode(&userPreferences)
	if err != nil {
		// If no preferences found, return departments as-is
		return departments
	}

	// Get department order for this community
	communityPref, exists := userPreferences.CommunityPreferences[communityID]
	if !exists || len(communityPref.DepartmentOrder) == 0 {
		// If no preferences for this community, return departments as-is
		return departments
	}

	// Create a map of department ID to order for quick lookup
	orderMap := make(map[string]int)
	for _, deptOrder := range communityPref.DepartmentOrder {
		orderMap[deptOrder.DepartmentID] = deptOrder.Order
	}

	// Create a copy of departments to sort
	sortedDepartments := make([]models.Department, len(departments))
	copy(sortedDepartments, departments)

	// Sort departments based on user preferences
	sort.SliceStable(sortedDepartments, func(i, j int) bool {
		orderI, existsI := orderMap[sortedDepartments[i].ID.Hex()]
		orderJ, existsJ := orderMap[sortedDepartments[j].ID.Hex()]

		// If both departments have preferences, sort by order
		if existsI && existsJ {
			return orderI < orderJ
		}

		// If only one has preferences, prioritize the one with preferences
		if existsI && !existsJ {
			return true
		}
		if !existsI && existsJ {
			return false
		}

		// If neither has preferences, maintain original order
		return i < j
	})

	return sortedDepartments
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
// Deprecated: Use FetchCommunityMembersByRoleIDHandlerV2 for paginated results with user details
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

// FetchCommunityMembersByRoleIDHandlerV2 returns paginated members of a role in a community with populated user details
func (c Community) FetchCommunityMembersByRoleIDHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	roleID := mux.Vars(r)["roleId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent abuse
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
		config.ErrorStatus("role not found", http.StatusNotFound, w, fmt.Errorf("role with ID %s not found in community", roleID))
		return
	}

	// Calculate pagination
	totalMembers := len(role.Members)
	offset := (page - 1) * limit
	end := offset + limit
	if end > totalMembers {
		end = totalMembers
	}
	if offset >= totalMembers {
		// Return empty result for pages beyond available data
		response := map[string]interface{}{
			"members": []interface{}{},
			"pagination": map[string]interface{}{
				"currentPage": page,
				"totalPages":  (totalMembers + limit - 1) / limit,
				"totalCount":  totalMembers,
				"hasNextPage": false,
				"hasPrevPage": page > 1,
			},
		}
		responseBytes, _ := json.Marshal(response)
		w.WriteHeader(http.StatusOK)
		w.Write(responseBytes)
		return
	}

	// Get paginated member IDs
	paginatedMemberIDs := role.Members[offset:end]

	// Populate user details for each member
	var populatedMembers []map[string]interface{}
	for _, memberID := range paginatedMemberIDs {
		// Convert memberID string to ObjectID
		memberObjectID, err := primitive.ObjectIDFromHex(memberID)
		if err != nil {
			// Skip invalid ObjectIDs
			continue
		}

		// Find user by ID
		userFilter := bson.M{"_id": memberObjectID}
		var user models.User
		err = c.UDB.FindOne(context.Background(), userFilter).Decode(&user)
		if err != nil {
			// Skip users that can't be found
			continue
		}

		// Check if user is verified (has active premium or premium+ subscription)
		isVerified := false
		if user.Details.Subscription.Active && (user.Details.Subscription.Plan == "premium" || user.Details.Subscription.Plan == "premium_plus") {
			isVerified = true
		}

		// Create member object with required fields
		member := map[string]interface{}{
			"id":             user.ID,
			"username":       user.Details.Username,
			"profilePicture": user.Details.ProfilePicture,
			"callSign":       user.Details.CallSign,
			"isVerified":     isVerified,
		}

		populatedMembers = append(populatedMembers, member)
	}

	// Calculate pagination info
	totalPages := (totalMembers + limit - 1) / limit
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	// Build response
	response := map[string]interface{}{
		"members": populatedMembers,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalCount":  totalMembers,
			"hasNextPage": hasNextPage,
			"hasPrevPage": hasPrevPage,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)
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

	// Sort departments based on user preferences first
	sortedDepartments := c.sortDepartmentsByUserPreferences(community.Details.Departments, userID, communityID)

	// Initialize the userDepartments slice
	var userDepartments []models.Department

	// Filter departments where the user is a member with status "approved" or approval is not required
	for _, department := range sortedDepartments {
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
	userID := r.URL.Query().Get("userId")

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

	// Sort departments based on user preferences if userID is provided
	sortedDepartments := c.sortDepartmentsByUserPreferences(community.Details.Departments, userID, communityID)

	// Return the departments array
	response := map[string]interface{}{
		"departments": sortedDepartments,
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
		// Step 1: Load the community document
		communityDoc, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
		if err != nil {
			config.ErrorStatus("community not found", http.StatusNotFound, w, err)
			return
		}

		// Step 2: Loop through departments to find the right one
		departments := communityDoc.Details.Departments
		var deptIndex = -1
		var userAlreadyExists = false

		for i, dep := range departments {

			if dep.ID == dID {
				deptIndex = i
				members := dep.Members
				for _, m := range members {

					if m.UserID == memberID {
						userAlreadyExists = true
						break
					}
				}
				break
			}
		}

		if deptIndex == -1 {
			config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
			return
		}

		if userAlreadyExists {
			config.ErrorStatus("member already exists in the department", http.StatusConflict, w, fmt.Errorf("member already exists in the department"))
			return
		}

		// Step 3: Add the member if not already there
		update := bson.M{
			"$addToSet": bson.M{
				fmt.Sprintf("community.departments.%d.members", deptIndex): bson.M{
					"_id":       primitive.NewObjectID(),
					"userID":    memberID,
					"status":    "approved",
					"tenCodeID": "",
				},
			},
		}

		err = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, update)
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

// GetEliteCommunitiesHandler returns a paginated list of elite communities
// Deprecated: Use FetchEliteCommunitiesHandler instead
func (c Community) GetEliteCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
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

	// Build the aggregation pipeline
	pipeline := mongo.Pipeline{
		// Match communities with "elite" subscription and "public" visibility
		{{"$match", bson.M{"community.subscription.plan": "elite", "community.visibility": "public"}}},

		// Add a random field for sorting
		{{"$addFields", bson.M{"randomSort": bson.M{"$rand": bson.M{}}}}},

		// Sort by the random field
		{{"$sort", bson.M{"randomSort": 1}}},

		// Skip and limit for pagination
		{{"$skip", skip}},
		{{"$limit", limit64}},
	}

	// Execute the aggregation
	cursor, err := c.DB.Aggregate(context.TODO(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch elite communities", http.StatusInternalServerError, w, err)
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
	totalCount, _ := c.DB.CountDocuments(context.TODO(), bson.M{"community.subscription.plan": "elite", "community.visibility": "public"})
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       communities,
	}

	// Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}

// FetchEliteCommunitiesHandler returns a paginated list of elite communities with reduced data for speed and performance
func (c Community) FetchEliteCommunitiesHandler(w http.ResponseWriter, r *http.Request) {
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

	// Aggregation pipeline for elite + public communities
	pipeline := mongo.Pipeline{
		{{"$match", bson.M{"community.subscription.plan": "elite", "community.visibility": "public"}}},
		{{"$addFields", bson.M{"randomSort": bson.M{"$rand": bson.M{}}}}},
		{{"$sort", bson.M{"randomSort": 1}}},
		{{"$skip", skip}},
		{{"$limit", limit64}},
	}

	cursor, err := c.DB.Aggregate(context.TODO(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch elite communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.TODO())

	// Decode all results
	var decodedCommunities []struct {
		ID        primitive.ObjectID `bson:"_id"`
		Community struct {
			Name                   string   `bson:"name"`
			ImageLink              string   `bson:"imageLink"`
			MembersCount           int      `bson:"membersCount"`
			Tags                   []string `bson:"tags"`
			PromotionalText        string   `bson:"promotionalText"`
			PromotionalDescription string   `bson:"promotionalDescription"`
		} `bson:"community"`
	}
	if err := cursor.All(context.TODO(), &decodedCommunities); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Trimmed down result structure
	var responseData []map[string]interface{}
	for _, item := range decodedCommunities {
		responseData = append(responseData, map[string]interface{}{
			"_id":                    item.ID,
			"name":                   item.Community.Name,
			"imageLink":              item.Community.ImageLink,
			"membersCount":           item.Community.MembersCount,
			"tags":                   item.Community.Tags,
			"promotionalText":        item.Community.PromotionalText,
			"promotionalDescription": item.Community.PromotionalDescription,
		})
	}

	// Count total matching documents
	totalCount, _ := c.DB.CountDocuments(context.TODO(), bson.M{
		"community.subscription.plan": "elite",
		"community.visibility":        "public",
	})

	// Return paginated response
	response := map[string]interface{}{
		"page":       Page,
		"totalCount": totalCount,
		"data":       responseData,
		"limit":      Limit,
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// SubscribeCommunityHandler subscribes a community to a specific tier
func (c Community) SubscribeCommunityHandler(w http.ResponseWriter, r *http.Request) {
	var requestBody struct {
		UserID                 string `json:"userId"`
		CommunityID            string `json:"communityId"`
		SubscriptionID         string `json:"subscriptionId"`
		Status                 string `json:"status"`
		Tier                   string `json:"tier"`
		IsAnnual               bool   `json:"isAnnual"`
		PromotionalText        string `json:"promotionalText"`
		PromotionalDescription string `json:"promotionalDescription"`
		PurchaseDate           string `json:"purchaseDate"`
		ExpirationDate         string `json:"expirationDate"`
		DurationMonths         int    `json:"durationMonths"`
	}

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	cID, err := primitive.ObjectIDFromHex(requestBody.CommunityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	isActive := requestBody.Status == "active"

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.subscriptionCreatedBy":       requestBody.UserID,
			"community.promotionalText":             requestBody.PromotionalText,
			"community.promotionalDescription":      requestBody.PromotionalDescription,
			"community.subscription.id":             requestBody.SubscriptionID,
			"community.subscription.plan":           requestBody.Tier,
			"community.subscription.isAnnual":       requestBody.IsAnnual,
			"community.subscription.active":         isActive,
			"community.subscription.purchaseDate":   requestBody.PurchaseDate,
			"community.subscription.expirationDate": requestBody.ExpirationDate,
			"community.subscription.durationMonths": requestBody.DurationMonths,
			"community.subscription.createdAt":      primitive.NewDateTimeFromTime(time.Now()),
			"community.subscription.updatedAt":      primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to subscribe community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Community subscribed successfully",
	})
}

// GetCommunityUserSubscriptions returns the list of communities a user is subscribed to
func (c Community) GetCommunityUserSubscriptions(w http.ResponseWriter, r *http.Request) {
	// Extract user_id from the path
	userID := mux.Vars(r)["user_id"]

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

	// Validate user_id
	// uID, err := primitive.ObjectIDFromHex(userID)
	// if err != nil {
	// 	config.ErrorStatus("invalid user ID", http.StatusBadRequest, w, err)
	// 	return
	// }

	// Build the filter to match communities with subscriptionCreatedBy equal to user_id
	filter := bson.M{"community.subscriptionCreatedBy": userID}

	// Count the total number of matching communities
	totalCount, err := c.DB.CountDocuments(context.TODO(), filter)
	if err != nil {
		config.ErrorStatus("failed to count communities", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch the paginated list of communities
	options := options.Find().SetSkip(skip).SetLimit(limit64)
	cursor, err := c.DB.Find(context.TODO(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
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
	paginatedResponse := PaginatedDataResponse{
		Page:       Page,
		TotalCount: totalCount,
		Data:       communities,
	}

	// Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(paginatedResponse)
}

// CancelCommunitySubscriptionHandler cancels a user's subscription
func (c Community) CancelCommunitySubscriptionHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CommunityID string `json:"communityId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if req.CommunityID == "" {
		config.ErrorStatus("community ID is required", http.StatusBadRequest, w, nil)
		return
	}

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(req.CommunityID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	// Retrieve the user from the database
	// comm := models.Community{}
	comm, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus(fmt.Sprint("failed to find community ", req.CommunityID), http.StatusInternalServerError, w, err)
		return
	}

	// Update the subscription in Stripe to cancel at the end of the period
	params := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	}
	sub, err := subscription.Update(comm.Details.Subscription.ID, params)
	if err != nil {
		config.ErrorStatus(fmt.Sprint("Failed to cancel subscription", req.CommunityID), http.StatusInternalServerError, w, err)
		return
	}

	cancelAtTime := time.Unix(sub.CancelAt, 0)
	cancelAtPrimitive := primitive.NewDateTimeFromTime(cancelAtTime)

	filter := bson.M{"_id": cID}
	update := bson.M{
		"$set": bson.M{
			"community.subscription.cancelAt":  cancelAtPrimitive,
			"community.subscription.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
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

// FetchCommunitiesByTagHandler returns communities by tag
// Deprecated: Use FetchCommunitiesByTagHandlerV2 instead
func (c Community) FetchCommunitiesByTagHandler(w http.ResponseWriter, r *http.Request) {
	tag := mux.Vars(r)["tag"]
	if tag == "" {
		config.ErrorStatus("tag is required", http.StatusBadRequest, w, nil)
		return
	}

	// Parse pagination parameters
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Default limit
	}

	// Step 1: Fetch a large random pool of public communities that match the tag
	pipeline := mongo.Pipeline{
		{{"$match", bson.D{
			{"community.visibility", "public"},
			{"community.tags", tag},
		}}},
		{{"$sample", bson.D{
			{"size", 50},
		}}},
		{{"$project", bson.D{
			{"community.name", 1},
			{"_id", 1},
			{"community.tags", 1},
			{"community.imageLink", 1},
			{"community.membersCount", 1},
			{"community.subscription", 1},
			{"community.visibility", 1},
		}}},
	}

	cursor, err := c.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var allCommunities []models.Community
	if err := cursor.All(context.Background(), &allCommunities); err != nil {
		config.ErrorStatus("failed to parse communities", http.StatusInternalServerError, w, err)
		return
	}

	// Step 2: Group communities by subscription plan
	var elites, premiums, standards, basics, others []models.Community
	for _, community := range allCommunities {
		plan := community.Details.Subscription.Plan
		switch plan {
		case "elite":
			elites = append(elites, community)
		case "premium":
			premiums = append(premiums, community)
		case "standard":
			standards = append(standards, community)
		case "basic":
			basics = append(basics, community)
		default:
			others = append(others, community)
		}
	}

	// Step 3: Assemble the final prioritized list
	var finalResults []models.Community
	pickRandom := func(list []models.Community) {
		if len(list) > 0 {
			rand.Seed(time.Now().UnixNano())
			finalResults = append(finalResults, list[rand.Intn(len(list))])
		}
	}

	pickRandom(elites)
	pickRandom(premiums)
	pickRandom(standards)
	pickRandom(basics)

	// Step 4: Fill the rest randomly from others
	remainingSlots := limit - len(finalResults)
	if remainingSlots > 0 && len(others) > 0 {
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(others), func(i, j int) {
			others[i], others[j] = others[j], others[i]
		})
		if remainingSlots > len(others) {
			remainingSlots = len(others)
		}
		finalResults = append(finalResults, others[:remainingSlots]...)
	}

	// Step 5: Deduplicate by _id
	unique := make(map[string]bool)
	var dedupedResults []models.Community
	for _, community := range finalResults {
		id := community.ID.Hex()
		if !unique[id] {
			unique[id] = true
			dedupedResults = append(dedupedResults, community)
		}
	}

	// Step 6: Send the response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(dedupedResults)
}

// FetchCommunitiesByTagHandlerV2 returns communities by tag with pagination and optimized data
func (c Community) FetchCommunitiesByTagHandlerV2(w http.ResponseWriter, r *http.Request) {
	tag := mux.Vars(r)["tag"]
	if tag == "" {
		config.ErrorStatus("tag is required", http.StatusBadRequest, w, nil)
		return
	}

	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 0 {
		page = 0
	}
	skip := int64(page * limit)

	// Struct for decoding
	type liteCommunity struct {
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

	// Build match stage
	matchStage := bson.D{{"community.visibility", "public"}}
	if tag != "all" {
		matchStage = append(matchStage, bson.E{"community.tags", tag})
	}

	// Aggregation pipeline with paging
	pipeline := mongo.Pipeline{
		{{"$match", matchStage}},
		{{"$sort", bson.D{{"community.name", 1}}}}, // consistent order for pagination
		{{"$skip", skip}},
		{{"$limit", int64(limit)}},
		{{"$project", bson.D{
			{"_id", 1},
			{"community.name", 1},
			{"community.imageLink", 1},
			{"community.membersCount", 1},
			{"community.tags", 1},
			{"community.promotionalText", 1},
			{"community.promotionalDescription", 1},
			{"community.subscription", 1},
		}}},
	}

	cursor, err := c.DB.Aggregate(context.Background(), pipeline)
	if err != nil {
		config.ErrorStatus("failed to fetch communities", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var results []liteCommunity
	if err := cursor.All(context.Background(), &results); err != nil {
		config.ErrorStatus("failed to decode communities", http.StatusInternalServerError, w, err)
		return
	}

	// Format response
	var data []map[string]interface{}
	for _, item := range results {
		data = append(data, map[string]interface{}{
			"_id":                    item.ID,
			"name":                   item.Community.Name,
			"imageLink":              item.Community.ImageLink,
			"membersCount":           item.Community.MembersCount,
			"tags":                   item.Community.Tags,
			"promotionalText":        item.Community.PromotionalText,
			"promotionalDescription": item.Community.PromotionalDescription,
			"subscription": map[string]interface{}{
				"active": item.Community.Subscription.Active,
				"plan":   item.Community.Subscription.Plan,
			},
		})
	}

	// Count total matching documents
	countFilter := bson.M{"community.visibility": "public"}
	if tag != "all" {
		countFilter["community.tags"] = tag
	}
	totalCount, _ := c.DB.CountDocuments(context.Background(), countFilter)

	// Return response
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tag":        tag,
		"limit":      limit,
		"page":       page,
		"totalCount": totalCount,
		"data":       data,
	})
}

// ArchiveCommunityHandler archives a community
func (c *Community) ArchiveCommunityHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["community_id"]

	// Fetch community details
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID: "+err.Error(), http.StatusBadRequest, w, err)
		return
	}

	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Failed to fetch community: "+err.Error(), http.StatusInternalServerError, w, err)
		return
	}

	// Write to archivedcommunities collection
	_, err = c.ADB.InsertOne(context.Background(), *community)
	if err != nil {
		config.ErrorStatus("Failed to archive community: "+err.Error(), http.StatusInternalServerError, w, err)
		return
	}

	// Delete from communities collection
	err = c.DB.DeleteOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Failed to delete community: "+err.Error(), http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success": true, "message": "Community archived successfully"}`))
}

// GetOnlineUsersHandler returns a list of online users in a community
func (c *Community) GetOnlineUsersHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default to page 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10 // Default limit
	}
	skip := (page - 1) * limit

	// Query for users with the specified communityId and status "online" in the communities array
	filter := bson.M{
		"user.communities": bson.M{
			"$elemMatch": bson.M{
				"communityId": communityID,
				"status":      "approved",
			},
		},
		"user.isOnline": true,
	}
	opts := options.Find().SetSkip(int64(skip)).SetLimit(int64(limit))

	cursor, err := c.UDB.Find(context.Background(), filter, opts)
	if err != nil {
		config.ErrorStatus("Failed to fetch online users", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(context.Background())

	var users []models.User
	if err := cursor.All(context.Background(), &users); err != nil {
		config.ErrorStatus("Failed to parse users", http.StatusInternalServerError, w, err)
		return
	}

	// Get the total count of online users
	total, err := c.UDB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("Failed to count online users", http.StatusInternalServerError, w, err)
		return
	}

	// Prepare the response
	response := struct {
		Total int           `json:"total"`
		Page  int           `json:"page"`
		Users []models.User `json:"users"`
	}{
		Total: int(total),
		Page:  page,
		Users: users,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetActiveTenCodeHandler returns the active Ten-Code for a user in a community
func (c Community) GetActiveTenCodeHandler(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	userID := r.URL.Query().Get("userId")

	if communityID == "" || userID == "" {
		config.ErrorStatus("communityId and userId are required", http.StatusBadRequest, w, fmt.Errorf("communityId and userId are required"))
		return
	}

	// Convert communityID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid communityId", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		} else {
			config.ErrorStatus("Failed to fetch community", http.StatusInternalServerError, w, err)
		}
		return
	}

	// Retrieve the user's tenCodeID from the members map
	memberDetails, exists := community.Details.Members[userID]
	if !exists {
		config.ErrorStatus("[GetActiveTenCodeHandler] User not found in community members", http.StatusNotFound, w, fmt.Errorf("user not found in community members"))
		return
	}
	tenCodeID := memberDetails.TenCodeID

	tenCodeIDObjectID, err := primitive.ObjectIDFromHex(tenCodeID)
	if err != nil {
		config.ErrorStatus("Invalid tenCodeId", http.StatusBadRequest, w, err)
		return
	}

	// Find the tenCode by ID
	var code, description string
	for _, tenCode := range community.Details.TenCodes {
		if tenCode.ID == tenCodeIDObjectID {
			code = tenCode.Code
			description = tenCode.Description
			break
		}
	}

	if code == "" || description == "" {
		config.ErrorStatus("TenCode not found", http.StatusNotFound, w, fmt.Errorf("tenCode not found"))
		return
	}

	// Return the response
	response := map[string]string{
		"code":        code,
		"description": description,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetPaginatedDepartmentsHandler returns a paginated list of departments by communityId
func (c Community) GetPaginatedDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	userID := r.URL.Query().Get("userId")

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default to page 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10 // Default limit
	}
	offset := (page - 1) * limit

	// Convert communityID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}

	// Sort departments based on user preferences first
	sortedDepartments := c.sortDepartmentsByUserPreferences(community.Details.Departments, userID, communityID)

	// Filter and paginate departments
	var filteredDepartments []map[string]interface{}
	for _, department := range sortedDepartments {
		if department.ApprovalRequired {
			// Check if user is in the members list with status "approved"
			isApproved := false
			for _, member := range department.Members {
				if member.UserID == userID && member.Status == "approved" {
					isApproved = true
					break
				}
			}
			if !isApproved {
				continue
			}
		}

		// Add department with only required fields
		departmentData := map[string]interface{}{
			"_id":         department.ID,
			"name":        department.Name,
			"description": department.Description,
			"image":       department.Image,
		}

		// Add template name if available (legacy template system)
		if department.Template.Name != "" {
			departmentData["templateName"] = department.Template.Name
		}

		filteredDepartments = append(filteredDepartments, departmentData)
	}

	// Apply pagination
	start := offset
	end := offset + limit
	if start > len(filteredDepartments) {
		start = len(filteredDepartments)
	}
	if end > len(filteredDepartments) {
		end = len(filteredDepartments)
	}
	paginatedDepartments := filteredDepartments[start:end]

	// Return the response
	response := map[string]interface{}{
		"page":       page,
		"limit":      limit,
		"totalCount": len(filteredDepartments),
		"data":       paginatedDepartments,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetNonMemberDepartmentsHandler returns a paginated list of departments that the user is not a member of
func (c Community) GetNonMemberDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	userID := r.URL.Query().Get("userId")

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default to page 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10 // Default limit
	}
	offset := (page - 1) * limit

	// Convert communityID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}

	// Filter departments that user is not a member of
	var filteredDepartments []map[string]interface{}
	for _, department := range community.Details.Departments {
		// Only include departments that require approval
		if !department.ApprovalRequired {
			continue
		}

		// Check if user is NOT in the members list or is not "approved"
		isMember := false
		for _, member := range department.Members {
			if member.UserID == userID {
				if member.Status == "approved" {
					isMember = true
				}
				break
			}
		}

		// If user is not a member (or not approved), include this department
		if !isMember {
			// Add department with required fields
			departmentData := map[string]interface{}{
				"_id":              department.ID,
				"name":             department.Name,
				"description":      department.Description,
				"image":            department.Image,
				"approvalRequired": department.ApprovalRequired,
			}

			// Add template name if available (legacy template system)
			if department.Template.Name != "" {
				departmentData["templateName"] = department.Template.Name
			}

			filteredDepartments = append(filteredDepartments, departmentData)
		}
	}

	// Apply pagination
	start := offset
	end := offset + limit
	if start > len(filteredDepartments) {
		start = len(filteredDepartments)
	}
	if end > len(filteredDepartments) {
		end = len(filteredDepartments)
	}
	paginatedDepartments := filteredDepartments[start:end]

	// Return the response
	response := map[string]interface{}{
		"page":       page,
		"limit":      limit,
		"totalCount": len(filteredDepartments),
		"data":       paginatedDepartments,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetPaginatedAllDepartmentsHandler returns a paginated list of all departments by communityId
func (c Community) GetPaginatedAllDepartmentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	userID := r.URL.Query().Get("userId")

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default to page 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10 // Default limit
	}
	offset := (page - 1) * limit

	// Convert communityID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("Invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("Community not found", http.StatusNotFound, w, err)
		return
	}

	// Sort departments based on user preferences first
	sortedDepartments := c.sortDepartmentsByUserPreferences(community.Details.Departments, userID, communityID)

	// Filter departments
	var filteredDepartments []map[string]interface{}
	for _, department := range sortedDepartments {
		if department.ApprovalRequired {
			// Check if user is not in the members list or status is not "approved"
			isMember := false
			for _, member := range department.Members {
				if member.UserID == userID && member.Status == "approved" {
					isMember = true
					break
				}
			}
			if isMember {
				continue
			}

			// Add department with only required fields
			filteredDepartments = append(filteredDepartments, map[string]interface{}{
				"_id":   department.ID,
				"name":  department.Name,
				"image": department.Image,
			})
		}
	}

	// Apply pagination
	start := offset
	end := offset + limit
	if start > len(filteredDepartments) {
		start = len(filteredDepartments)
	}
	if end > len(filteredDepartments) {
		end = len(filteredDepartments)
	}
	paginatedDepartments := filteredDepartments[start:end]

	// Return the response
	response := map[string]interface{}{
		"page":       page,
		"limit":      limit,
		"totalCount": len(filteredDepartments),
		"data":       paginatedDepartments,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// FetchBannedUsersHandlerV2 returns paginated banned users of a community with populated user details
func (c Community) FetchBannedUsersHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent abuse
	}

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

	// Get the list of banned user IDs
	banList := community.Details.BanList

	// Calculate pagination
	totalBannedUsers := len(banList)
	offset := (page - 1) * limit
	end := offset + limit
	if end > totalBannedUsers {
		end = totalBannedUsers
	}
	if offset >= totalBannedUsers {
		// Return empty result for pages beyond available data
		response := map[string]interface{}{
			"bannedUsers": []interface{}{},
			"pagination": map[string]interface{}{
				"currentPage": page,
				"totalPages":  (totalBannedUsers + limit - 1) / limit,
				"totalCount":  totalBannedUsers,
				"hasNextPage": false,
				"hasPrevPage": page > 1,
			},
		}
		responseBytes, _ := json.Marshal(response)
		w.WriteHeader(http.StatusOK)
		w.Write(responseBytes)
		return
	}

	// Get paginated banned user IDs
	paginatedBanList := banList[offset:end]

	// Convert paginated banList to a slice of primitive.ObjectID
	var objectIDs []primitive.ObjectID
	for _, id := range paginatedBanList {
		objID, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			// Skip invalid ObjectIDs
			continue
		}
		objectIDs = append(objectIDs, objID)
	}

	if len(objectIDs) == 0 {
		// Return empty result if no valid ObjectIDs
		response := map[string]interface{}{
			"bannedUsers": []interface{}{},
			"pagination": map[string]interface{}{
				"currentPage": page,
				"totalPages":  (totalBannedUsers + limit - 1) / limit,
				"totalCount":  totalBannedUsers,
				"hasNextPage": page < (totalBannedUsers+limit-1)/limit,
				"hasPrevPage": page > 1,
			},
		}
		responseBytes, _ := json.Marshal(response)
		w.WriteHeader(http.StatusOK)
		w.Write(responseBytes)
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

	// Populate user details for each banned user
	var populatedBannedUsers []map[string]interface{}
	for _, user := range bannedUsers {
		// Check if user is verified (has active premium or premium+ subscription)
		isVerified := false
		if user.Details.Subscription.Active && (user.Details.Subscription.Plan == "premium" || user.Details.Subscription.Plan == "premium_plus") {
			isVerified = true
		}

		// Create banned user object with required fields
		bannedUser := map[string]interface{}{
			"id":             user.ID,
			"username":       user.Details.Username,
			"profilePicture": user.Details.ProfilePicture,
			"callSign":       user.Details.CallSign,
			"isVerified":     isVerified,
		}

		populatedBannedUsers = append(populatedBannedUsers, bannedUser)
	}

	// Calculate pagination info
	totalPages := (totalBannedUsers + limit - 1) / limit
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	// Build response
	response := map[string]interface{}{
		"bannedUsers": populatedBannedUsers,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalCount":  totalBannedUsers,
			"hasNextPage": hasNextPage,
			"hasPrevPage": hasPrevPage,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)
}

// TransferCommunityOwnershipHandler handles the transfer of community ownership
func (c Community) TransferCommunityOwnershipHandler(w http.ResponseWriter, r *http.Request) {
	// Get community ID from URL parameters
	communityID := mux.Vars(r)["communityId"]

	// Parse request body
	var transferRequest struct {
		CurrentUserID string `json:"currentUserId"`
		NewOwnerID    string `json:"newOwnerId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&transferRequest); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate required fields
	if transferRequest.CurrentUserID == "" || transferRequest.NewOwnerID == "" {
		config.ErrorStatus("currentUserId and newOwnerId are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	// Convert community ID to ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Fetch the community
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Check if current user is the owner
	if community.Details.OwnerID != transferRequest.CurrentUserID {
		config.ErrorStatus("only the current owner can transfer ownership", http.StatusForbidden, w, fmt.Errorf("user is not the community owner"))
		return
	}

	// Check if current user has Head Admin role with administrator permission enabled
	hasPermission := false
	for _, role := range community.Details.Roles {
		if role.Name == "Head Admin" {
			// Check if current user is a member of this role
			isMember := false
			for _, member := range role.Members {
				if member == transferRequest.CurrentUserID {
					isMember = true
					break
				}
			}

			if isMember {
				// Check if administrator permission is enabled
				for _, permission := range role.Permissions {
					if permission.Name == "administrator" && permission.Enabled {
						hasPermission = true
						break
					}
				}
			}
			break
		}
	}

	if !hasPermission {
		config.ErrorStatus("user does not have permission to transfer ownership", http.StatusForbidden, w, fmt.Errorf("user lacks required permissions"))
		return
	}

	// Validate that new owner exists
	newOwnerID, err := primitive.ObjectIDFromHex(transferRequest.NewOwnerID)
	if err != nil {
		config.ErrorStatus("invalid new owner ID", http.StatusBadRequest, w, err)
		return
	}

	// Check if new owner exists
	var newOwner models.User
	err = c.UDB.FindOne(context.Background(), bson.M{"_id": newOwnerID}).Decode(&newOwner)
	if err != nil {
		config.ErrorStatus("new owner not found", http.StatusNotFound, w, err)
		return
	}

	// Update the community with new owner
	update := bson.M{
		"$set": bson.M{
			"community.ownerID":   transferRequest.NewOwnerID,
			"community.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	// Update the Head Admin role members to include the new owner
	// Find the Head Admin role and update its members
	for i, role := range community.Details.Roles {
		if role.Name == "Head Admin" {
			// Remove current owner from members if they're not the new owner
			if transferRequest.CurrentUserID != transferRequest.NewOwnerID {
				// Remove current owner from members
				var newMembers []string
				for _, member := range role.Members {
					if member != transferRequest.CurrentUserID {
						newMembers = append(newMembers, member)
					}
				}
				// Add new owner to members if not already present
				hasNewOwner := false
				for _, member := range newMembers {
					if member == transferRequest.NewOwnerID {
						hasNewOwner = true
						break
					}
				}
				if !hasNewOwner {
					newMembers = append(newMembers, transferRequest.NewOwnerID)
				}

				// Update the role members
				update["$set"].(bson.M)[fmt.Sprintf("community.roles.%d.members", i)] = newMembers
			}
			break
		}
	}

	// Apply the update
	err = c.DB.UpdateOne(context.Background(), bson.M{"_id": cID}, update)
	if err != nil {
		config.ErrorStatus("failed to transfer ownership", http.StatusInternalServerError, w, err)
		return
	}

	// Return success response
	response := map[string]interface{}{
		"message":    "Community ownership transferred successfully",
		"newOwnerId": transferRequest.NewOwnerID,
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)
}

// FetchCommunityMembersHandlerV2 returns paginated members of a community with populated user details
func (c Community) FetchCommunityMembersHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent abuse
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
		config.ErrorStatus("failed to count users", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch users with pagination
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

	// Populate user details for each member
	var populatedMembers []map[string]interface{}
	for _, user := range users {
		// Check if user is verified (has active premium or premium+ subscription)
		isVerified := false
		if user.Details.Subscription.Active && (user.Details.Subscription.Plan == "premium" || user.Details.Subscription.Plan == "premium_plus") {
			isVerified = true
		}

		// Create member object with required fields
		member := map[string]interface{}{
			"id":             user.ID,
			"username":       user.Details.Username,
			"profilePicture": user.Details.ProfilePicture,
			"callSign":       user.Details.CallSign,
			"isVerified":     isVerified,
		}

		populatedMembers = append(populatedMembers, member)
	}

	// Calculate pagination info
	totalPages := (totalUsers + int64(limit) - 1) / int64(limit)
	hasNextPage := page < int(totalPages)
	hasPrevPage := page > 1

	// Build response
	response := map[string]interface{}{
		"members": populatedMembers,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"totalPages":  totalPages,
			"totalCount":  totalUsers,
			"hasNextPage": hasNextPage,
			"hasPrevPage": hasPrevPage,
		},
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		config.ErrorStatus("failed to marshal response", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(responseBytes)
}

// FetchCommunityMembersExcludeRoleHandlerV2 fetches community members who are NOT in a specific role
func (c *Community) FetchCommunityMembersExcludeRoleHandlerV2(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	roleID := vars["roleId"]

	// Parse pagination parameters
	page := 1
	limit := 20
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if limit > 100 {
		limit = 100
	}

	// Validate ObjectIDs
	if !primitive.IsValidObjectID(communityID) {
		http.Error(w, "Invalid community ID", http.StatusBadRequest)
		return
	}
	if !primitive.IsValidObjectID(roleID) {
		http.Error(w, "Invalid role ID", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Step 1: Get the role to find current members
	communityObjID, _ := primitive.ObjectIDFromHex(communityID)
	community, err := c.DB.FindOne(ctx, bson.M{"_id": communityObjID})
	if err != nil {
		if err == mongo.ErrNoDocuments {
			http.Error(w, "Community not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to fetch community", http.StatusInternalServerError)
		return
	}

	// Find the specific role
	var targetRole *models.Role
	for _, role := range community.Details.Roles {
		if role.ID.Hex() == roleID {
			targetRole = &role
			break
		}
	}

	if targetRole == nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	// Step 2: Get all community members (approved status)
	communityFilter := bson.M{
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

	// Count total community members
	totalCount, err := c.UDB.CountDocuments(ctx, communityFilter)
	if err != nil {
		http.Error(w, "Failed to count community members", http.StatusInternalServerError)
		return
	}

	// Step 3: Get paginated community members
	skip := int64((page - 1) * limit)
	findOptions := options.Find().SetSkip(skip).SetLimit(int64(limit))

	cursor, err := c.UDB.Find(ctx, communityFilter, findOptions)
	if err != nil {
		http.Error(w, "Failed to fetch community members", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		http.Error(w, "Failed to decode users", http.StatusInternalServerError)
		return
	}

	// Step 4: Filter out users who are in the target role
	var filteredUsers []map[string]interface{}
	for _, user := range users {
		// Check if user is NOT in the target role
		isInRole := false
		for _, memberID := range targetRole.Members {
			if memberID == user.ID {
				isInRole = true
				break
			}
		}

		// Only include users NOT in the role
		if !isInRole {
			userData := map[string]interface{}{
				"id":             user.ID,
				"username":       user.Details.Username,
				"profilePicture": user.Details.ProfilePicture,
				"callSign":       user.Details.CallSign,
				"isVerified":     user.Details.Subscription.Active,
			}
			filteredUsers = append(filteredUsers, userData)
		}
	}

	// Calculate pagination info
	totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	        response := map[string]interface{}{
                "members": filteredUsers,
                "pagination": map[string]interface{}{
                        "currentPage": page,
                        "totalPages":  totalPages,
                        "totalCount":  totalCount,
                        "hasNextPage": hasNextPage,
                        "hasPrevPage": hasPrevPage,
                },
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)
}

// UpdateDepartmentComponentsHandler updates the components of a department with pagination
func (c Community) UpdateDepartmentComponentsHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Parse pagination parameters
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10 // Default limit
	}
	if limit > 100 {
		limit = 100 // Max limit
	}

	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1 // Default page
	}
	skip := (page - 1) * limit

	// Parse request body for component updates
	var requestBody struct {
		Components []models.Component `json:"components"`
	}
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate required fields
	if len(requestBody.Components) == 0 {
		config.ErrorStatus("components array is required", http.StatusBadRequest, w, fmt.Errorf("components array cannot be empty"))
		return
	}

	// Convert IDs to ObjectIDs
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}
	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the community and department
	community, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("failed to find community", http.StatusNotFound, w, err)
		return
	}

	// Find the department
	var targetDepartment *models.Department
	for i, dept := range community.Details.Departments {
		if dept.ID == dID {
			targetDepartment = &community.Details.Departments[i]
			break
		}
	}

	if targetDepartment == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, fmt.Errorf("department not found"))
		return
	}

	// Update components with pagination
	components := targetDepartment.Template.Components
	if components == nil {
		components = []models.Component{}
	}

	// Create a map for efficient lookup of existing components
	componentMap := make(map[string]models.Component)
	for _, comp := range components {
		componentMap[comp.ID.Hex()] = comp
	}

	// Update existing components based on the request
	for _, newComp := range requestBody.Components {
		if existingComp, exists := componentMap[newComp.ID.Hex()]; exists {
			existingComp.Name = newComp.Name
			existingComp.Enabled = newComp.Enabled
			componentMap[newComp.ID.Hex()] = existingComp
		}
	}

	// Convert back to array format for database storage
	updatedComponents := make([]models.Component, 0, len(componentMap))
	for _, comp := range componentMap {
		updatedComponents = append(updatedComponents, comp)
	}

	// Apply pagination to the updated components for response
	totalComponents := len(updatedComponents)
	startIndex := skip
	endIndex := skip + limit

	var paginatedComponents []models.Component
	if startIndex >= totalComponents {
		paginatedComponents = []models.Component{}
	} else {
		if endIndex > totalComponents {
			endIndex = totalComponents
		}
		paginatedComponents = updatedComponents[startIndex:endIndex]
	}

	// Calculate pagination metadata
	totalPages := (totalComponents + limit - 1) / limit
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	// Update the department in the database with the full components array
	filter := bson.M{"_id": cID, "community.departments._id": dID}
	update := bson.M{
		"$set": bson.M{
			"community.departments.$.template.components": updatedComponents,
			"community.departments.$.updatedAt":           primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update department components", http.StatusInternalServerError, w, err)
		return
	}

	// Build response with pagination
	response := map[string]interface{}{
		"components": paginatedComponents,
		"pagination": map[string]interface{}{
			"currentPage": page,
			"limit":       limit,
			"totalCount":  totalComponents,
			"totalPages":  totalPages,
			"hasNextPage": hasNextPage,
			"hasPrevPage": hasPrevPage,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetCommunityInviteCodesHandlerV2 returns paginated invite codes for a community with populated user details
func (c Community) GetCommunityInviteCodesHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent abuse
	}

	// Calculate the offset for pagination
	offset := (page - 1) * limit

	// Find invite codes for this community
	filter := bson.M{"communityId": communityID}

	// Count total invite codes
	totalInviteCodes, err := c.IDB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count invite codes", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch invite codes with pagination
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit)).SetSort(bson.D{{"createdAt", -1}})
	inviteCodes, err := c.IDB.Find(context.Background(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get invite codes", http.StatusInternalServerError, w, err)
		return
	}

	// Populate user details for each invite code
	var populatedInviteCodes []map[string]interface{}
	for _, inviteCode := range inviteCodes {
		// Get user details for createdBy
		var createdByUser map[string]interface{}
		if inviteCode.CreatedBy != "" {
			userObjID, err := primitive.ObjectIDFromHex(inviteCode.CreatedBy)
			if err == nil {
				var user models.User
				err = c.UDB.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
				if err == nil {
					createdByUser = map[string]interface{}{
						"id":       user.ID,
						"username": user.Details.Username,
						"email":    user.Details.Email,
					}
				}
			}
		}

		// Build the populated invite code
		populatedInviteCode := map[string]interface{}{
			"_id":            inviteCode.ID,
			"code":           inviteCode.Code,
			"communityId":    inviteCode.CommunityID,
			"expiresAt":      inviteCode.ExpiresAt,
			"maxUses":        inviteCode.MaxUses,
			"remainingUses":  inviteCode.RemainingUses,
			"createdBy":      inviteCode.CreatedBy,
			"createdByUser":  createdByUser,
			"createdAt":      inviteCode.CreatedAt,
		}

		populatedInviteCodes = append(populatedInviteCodes, populatedInviteCode)
	}

	// Calculate pagination metadata
	totalPages := int((totalInviteCodes + int64(limit) - 1) / int64(limit))
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	// Build response
	response := map[string]interface{}{
		"inviteCodes": populatedInviteCodes,
		"pagination": map[string]interface{}{
			"currentPage":  page,
			"totalPages":   totalPages,
			"totalCount":   totalInviteCodes,
			"hasNextPage":  hasNextPage,
			"hasPrevPage":  hasPrevPage,
			"limit":        limit,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DeleteInviteCodeHandler deletes an invite code by ID
func (c Community) DeleteInviteCodeHandler(w http.ResponseWriter, r *http.Request) {
	inviteCodeID := mux.Vars(r)["inviteCodeId"]

	// Convert string ID to ObjectID
	objID, err := primitive.ObjectIDFromHex(inviteCodeID)
	if err != nil {
		config.ErrorStatus("invalid invite code ID", http.StatusBadRequest, w, err)
		return
	}

	// First, get the invite code to find the community ID
	inviteCode, err := c.IDB.FindOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		config.ErrorStatus("invite code not found", http.StatusNotFound, w, err)
		return
	}

	// Delete the invite code
	err = c.IDB.DeleteOne(context.Background(), bson.M{"_id": objID})
	if err != nil {
		config.ErrorStatus("failed to delete invite code", http.StatusInternalServerError, w, err)
		return
	}

	// Remove the invite code ID from the community's inviteCodeIds array
	communityObjID, err := primitive.ObjectIDFromHex(inviteCode.CommunityID)
	if err == nil {
		c.DB.UpdateOne(
			context.Background(),
			bson.M{"_id": communityObjID},
			bson.M{"$pull": bson.M{"community.inviteCodeIds": inviteCodeID}},
		)
	}

	// Return success response
	response := map[string]interface{}{
		"success": true,
		"message": "Invite code deleted successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetCommunityCiviliansHandlerV2 returns paginated civilians for a community
func (c Community) GetCommunityCiviliansHandlerV2(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse pagination parameters
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100 // Cap at 100 to prevent abuse
	}

	// Calculate the offset for pagination
	offset := (page - 1) * limit

	// Find civilians for this community
	filter := bson.M{"civilian.activeCommunityID": communityID}

	// Count total civilians
	totalCivilians, err := c.CDB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count civilians", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch civilians with pagination
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit)).SetSort(bson.D{{"civilian.createdAt", -1}})
	civilians, err := c.CDB.Find(context.Background(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get civilians", http.StatusInternalServerError, w, err)
		return
	}

	// Populate user details for each civilian
	var populatedCivilians []map[string]interface{}
	for _, civilian := range civilians {
		// Get user details for userID
		var userDetails map[string]interface{}
		if civilian.Details.UserID != "" {
			userObjID, err := primitive.ObjectIDFromHex(civilian.Details.UserID)
			if err == nil {
				var user models.User
				err = c.UDB.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
				if err == nil {
					userDetails = map[string]interface{}{
						"id":       user.ID,
						"username": user.Details.Username,
						"email":    user.Details.Email,
					}
				}
			}
		}

		// Build the populated civilian
		populatedCivilian := map[string]interface{}{
			"_id":      civilian.ID,
			"civilian": civilian.Details,
			"user":     userDetails,
			"__v":      civilian.Version,
		}

		populatedCivilians = append(populatedCivilians, populatedCivilian)
	}

	// Calculate pagination metadata
	totalPages := int((totalCivilians + int64(limit) - 1) / int64(limit))
	hasNextPage := page < totalPages
	hasPrevPage := page > 1

	// Build response
	response := map[string]interface{}{
		"civilians": populatedCivilians,
		"pagination": map[string]interface{}{
			"currentPage":  page,
			"totalPages":   totalPages,
			"totalCount":   totalCivilians,
			"hasNextPage":  hasNextPage,
			"hasPrevPage":  hasPrevPage,
			"limit":        limit,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// SearchCommunityMembersHandler searches for community members by callSign or username
func (c *Community) SearchCommunityMembersHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	// Get search query parameter
	query := r.URL.Query().Get("q")
	if query == "" {
		config.ErrorStatus("query param q is required", http.StatusBadRequest, w, fmt.Errorf("q parameter is required"))
		return
	}

	// Parse pagination parameters
	page := 1
	limit := 10 // Default limit as requested
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	// Cap limit at 100 to prevent abuse
	if limit > 100 {
		limit = 100
	}

	// Validate community ID
	if !primitive.IsValidObjectID(communityID) {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, fmt.Errorf("invalid community ID format"))
		return
	}

	ctx := r.Context()

	// Build the complex search filter
	// We need to find users who:
	// 1. Have the community in their communities array with status "approved"
	// 2. Match the search query in their callSign or username
	filter := bson.M{
		"$and": []bson.M{
			// User must be a member of the community with status "approved"
			{
				"user.communities": bson.M{
					"$elemMatch": bson.M{
						"communityId": communityID,
						"status":      "approved",
					},
				},
			},
			// User must match the search query in callSign or username
			{
				"$or": []bson.M{
					{"user.callSign": bson.M{"$regex": query, "$options": "i"}},
					{"user.username": bson.M{"$regex": query, "$options": "i"}},
				},
			},
		},
	}

	// Count total matching users
	totalCount, err := c.UDB.CountDocuments(ctx, filter)
	if err != nil {
		config.ErrorStatus("failed to count community members", http.StatusInternalServerError, w, err)
		return
	}

	// Calculate pagination
	skip := int64((page - 1) * limit)
	totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))

	// Fetch paginated results
	findOptions := options.Find().
		SetSkip(skip).
		SetLimit(int64(limit)).
		SetSort(bson.M{"user.name": 1}) // Sort by name for consistent results

	cursor, err := c.UDB.Find(ctx, filter, findOptions)
	if err != nil {
		config.ErrorStatus("failed to search community members", http.StatusInternalServerError, w, err)
		return
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err = cursor.All(ctx, &users); err != nil {
		config.ErrorStatus("failed to decode users", http.StatusInternalServerError, w, err)
		return
	}

	// Populate user details for each member (same structure as FetchCommunityMembersHandlerV2)
	var populatedMembers []map[string]interface{}
	for _, user := range users {
		// Check if user is verified (has active premium or premium+ subscription)
		isVerified := false
		if user.Details.Subscription.Active && (user.Details.Subscription.Plan == "premium" || user.Details.Subscription.Plan == "premium_plus") {
			isVerified = true
		}

		// Create member object with required fields
		member := map[string]interface{}{
			"id":             user.ID,
			"username":       user.Details.Username,
			"profilePicture": user.Details.ProfilePicture,
			"callSign":       user.Details.CallSign,
			"isVerified":     isVerified,
		}

		populatedMembers = append(populatedMembers, member)
	}

	// Build response
	response := map[string]interface{}{
		"members": populatedMembers,
		"pagination": map[string]interface{}{
			"currentPage":  page,
			"totalPages":   totalPages,
			"totalCount":   totalCount,
			"hasNextPage":  page < totalPages,
			"hasPrevPage":  page > 1,
			"limit":        limit,
		},
		"query": query,
	}

        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(response); err != nil {
                config.ErrorStatus("failed to encode response", http.StatusInternalServerError, w, err)                                                         
                return
        }
}

// CreatePanicAlertHandler creates a new panic alert for a user in a community
func (c Community) CreatePanicAlertHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	// Parse community ID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse request body
	var request struct {
		UserID        string `json:"userId"`
		Username      string `json:"username"`
		CallSign      string `json:"callSign"`
		DepartmentType string `json:"departmentType"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Validate required fields
	if request.UserID == "" || request.Username == "" || request.CallSign == "" || request.DepartmentType == "" {
		config.ErrorStatus("userId, username, callSign, and departmentType are required", http.StatusBadRequest, w, fmt.Errorf("missing required fields"))
		return
	}

	// Generate unique alert ID
	alertID := primitive.NewObjectID().Hex()

	// Create panic alert
	panicAlert := models.PanicAlert{
		AlertID:       alertID,
		UserID:        request.UserID,
		Username:      request.Username,
		CallSign:      request.CallSign,
		DepartmentType: request.DepartmentType,
		CommunityID:   communityID,
		TriggeredAt:   primitive.NewDateTimeFromTime(time.Now()),
		Status:        "active",
		ClearedBy:     nil,
		ClearedAt:     nil,
	}

	// Add panic alert to community
	filter := bson.M{"_id": cID}
	update := bson.M{
		"$push": bson.M{
			"community.activePanicAlerts": panicAlert,
		},
		"$set": bson.M{
			"community.updatedAt": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to create panic alert", http.StatusInternalServerError, w, err)
		return
	}

	// Emit socket event for panic alert created
	alertData := map[string]interface{}{
		"alertId":       alertID,
		"userId":        request.UserID,
		"username":      request.Username,
		"callSign":      request.CallSign,
		"departmentType": request.DepartmentType,
		"communityId":   communityID,
		"triggeredAt":   panicAlert.TriggeredAt,
		"status":        "active",
	}
	EmitPanicAlertCreated(communityID, alertData)

	// Response
	response := map[string]interface{}{
		"success": true,
		"alertId": alertID,
		"message": "Panic alert created successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetPanicAlertsHandler retrieves panic alerts for a community
func (c Community) GetPanicAlertsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]

	// Parse community ID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Get status filter from query parameter
	status := r.URL.Query().Get("status")

	// Get community
	filter := bson.M{"_id": cID}
	community, err := c.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to get community", http.StatusNotFound, w, err)
		return
	}

	// Filter alerts by status if specified
	var filteredAlerts []models.PanicAlert
	for _, alert := range community.Details.ActivePanicAlerts {
		if status == "" || alert.Status == status {
			filteredAlerts = append(filteredAlerts, alert)
		}
	}

	// Response
	response := map[string]interface{}{
		"success": true,
		"alerts":  filteredAlerts,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ClearPanicAlertHandler clears a specific panic alert
func (c Community) ClearPanicAlertHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	alertID := vars["alertId"]

	// Parse community ID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse request body
	var request struct {
		ClearedBy string `json:"clearedBy"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.ClearedBy == "" {
		config.ErrorStatus("clearedBy is required", http.StatusBadRequest, w, fmt.Errorf("clearedBy is required"))
		return
	}

	// Update the specific panic alert
	filter := bson.M{"_id": cID, "community.activePanicAlerts.alertId": alertID}
	update := bson.M{
		"$set": bson.M{
			"community.activePanicAlerts.$.status":    "cleared",
			"community.activePanicAlerts.$.clearedBy": request.ClearedBy,
			"community.activePanicAlerts.$.clearedAt":  primitive.NewDateTimeFromTime(time.Now()),
			"community.updatedAt":                       primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to clear panic alert", http.StatusInternalServerError, w, err)
		return
	}

	// Emit socket event for panic button cleared
	EmitPanicButtonCleared(communityID, alertID)

	// Response
	response := map[string]interface{}{
		"success": true,
		"message": "Panic alert cleared successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ClearUserPanicAlertsHandler clears all panic alerts for a specific user
func (c Community) ClearUserPanicAlertsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	communityID := vars["communityId"]
	userID := vars["userId"]

	// Parse community ID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Parse request body
	var request struct {
		ClearedBy string `json:"clearedBy"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	if request.ClearedBy == "" {
		config.ErrorStatus("clearedBy is required", http.StatusBadRequest, w, fmt.Errorf("clearedBy is required"))
		return
	}

	// Get community to find user's active alerts
	filter := bson.M{"_id": cID}
	community, err := c.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to get community", http.StatusNotFound, w, err)
		return
	}

	// Find and update all active alerts for this user
	now := primitive.NewDateTimeFromTime(time.Now())
	for i, alert := range community.Details.ActivePanicAlerts {
		if alert.UserID == userID && alert.Status == "active" {
			community.Details.ActivePanicAlerts[i].Status = "cleared"
			community.Details.ActivePanicAlerts[i].ClearedBy = &request.ClearedBy
			community.Details.ActivePanicAlerts[i].ClearedAt = &now
		}
	}

	// Update community with cleared alerts
	update := bson.M{
		"$set": bson.M{
			"community.activePanicAlerts": community.Details.ActivePanicAlerts,
			"community.updatedAt":           primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to clear user panic alerts", http.StatusInternalServerError, w, err)
		return
	}

	// Emit socket event for panic button cleared
	EmitPanicButtonCleared(communityID, userID)

	// Response
	response := map[string]interface{}{
		"success": true,
		"message": "User panic alerts cleared successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
