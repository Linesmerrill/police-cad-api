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

	zap.S().Debugf("community_id: %v", commID)

	cID, err := primitive.ObjectIDFromHex(commID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	dbResp, err := c.DB.FindOne(context.Background(), bson.M{"_id": cID})
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

	// Insert the new community into the database
	_ = c.DB.InsertOne(context.Background(), newCommunity)

	// Add the community to the user's communities array
	ownerID := newCommunity.Details.OwnerID
	uID, err := primitive.ObjectIDFromHex(ownerID)
	if err != nil {
		config.ErrorStatus("failed to get objectID from Hex", http.StatusBadRequest, w, err)
		return
	}

	filter := bson.M{"_id": uID}
	update := bson.M{"$addToSet": bson.M{"user.communities": newCommunity.ID.Hex()}} // $addToSet ensures no duplicates
	_, err = c.UDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update user's communities", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte(`{"message": "Community created successfully"}`))
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

	cursor := c.DB.Find(context.Background(), filter, options)
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
	filter := bson.M{"user.communities": communityID}

	// Count the total number of users
	totalUsers, err := c.UDB.CountDocuments(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to count users by community ID", http.StatusInternalServerError, w, err)
		return
	}

	// Fetch only the first 10 users' details
	options := options.Find().SetSkip(int64(offset)).SetLimit(int64(limit))
	users, err := c.UDB.Find(context.Background(), filter, options)
	if err != nil {
		config.ErrorStatus("failed to get users by community ID", http.StatusInternalServerError, w, err)
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
	var updatedEvent models.Event
	if err := json.NewDecoder(r.Body).Decode(&updatedEvent); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Set the updatedAt field to the current time
	updatedEvent.UpdatedAt = primitive.NewDateTimeFromTime(time.Now())

	// Update the event in the community
	filter := bson.M{"_id": cID, "community.events._id": eID}
	update := bson.M{"$set": bson.M{
		"community.events.$.title":         updatedEvent.Title,
		"community.events.$.description":   updatedEvent.Description,
		"community.events.$.scheduledDate": updatedEvent.ScheduledDate,
		"community.events.$.image":         updatedEvent.Image,
		"community.events.$.location":      updatedEvent.Location,
		"community.events.$.required":      updatedEvent.Required,
		"community.events.$.updatedAt":     updatedEvent.UpdatedAt,
	}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
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

	// Parse the request body to get the new members
	var members []string
	if err := json.NewDecoder(r.Body).Decode(&members); err != nil {
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

	// Update the role members in the community
	filter := bson.M{"_id": cID, "community.roles._id": rID}
	update := bson.M{"$set": bson.M{"community.roles.$.members": members}}
	err = c.DB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to update role members", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"message": "Role members updated successfully"}`))
}
