package handlers

import (
	"context"
	"time"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
)

// Notification types emitted when a join request is resolved.
//
// Until these existed the requester was told nothing at all. Both
// AddCommunityToUserHandler and UpdateDepartmentJoinRequestHandler cleared the
// *admins'* join_request notification on resolution and sent the person who
// asked to join no message, so the only way to find out you had been let in was
// to revisit the community page and notice the buttons had changed. Communities
// here are run on Discord and the CAD is a supplemental tool, so a member who is
// not told often never comes back to look.
const (
	NotificationCommunityApproved  = "community_approved"
	NotificationCommunityDeclined  = "community_declined"
	NotificationDepartmentApproved = "department_approved"
	NotificationDepartmentDeclined = "department_declined"
)

// Join request statuses that count as a resolution.
const (
	JoinStatusApproved = "approved"
	JoinStatusDeclined = "declined"
)

// JoinResolution describes a join request that has just been approved or
// declined. DepartmentID is empty for a community-level request, which matches
// the data3 convention already used by the join_request notifications these
// resolve.
type JoinResolution struct {
	Status         string
	PreviousStatus string
	RequesterID    string
	ActorID        string
	CommunityID    string
	CommunityName  string
	DepartmentID   string
	DepartmentName string
}

// IsDepartment reports whether this resolution concerns a department join
// request rather than a community-level one.
func (j JoinResolution) IsDepartment() bool { return j.DepartmentID != "" }

// BuildJoinResolvedNotification returns the notification to send the requester,
// and whether one should be sent at all.
//
// It returns false when nothing actually changed. Approving an already-approved
// member is a no-op an admin can trigger by double-clicking, and announcing it
// again would tell someone they had been let into a community they have been in
// for weeks.
func BuildJoinResolvedNotification(res JoinResolution) (models.Notification, bool) {
	if res.RequesterID == "" || res.CommunityID == "" {
		return models.Notification{}, false
	}
	if res.Status == res.PreviousStatus {
		return models.Notification{}, false
	}

	communityName := res.CommunityName
	if communityName == "" {
		communityName = "the community"
	}
	departmentName := res.DepartmentName
	if departmentName == "" {
		departmentName = "the department"
	}

	var notifType, message string
	switch {
	case res.Status == JoinStatusApproved && res.IsDepartment():
		notifType = NotificationDepartmentApproved
		message = "You were approved for " + departmentName + " in " + communityName
	case res.Status == JoinStatusDeclined && res.IsDepartment():
		notifType = NotificationDepartmentDeclined
		message = "Your request to join " + departmentName + " in " + communityName + " was declined"
	case res.Status == JoinStatusApproved:
		notifType = NotificationCommunityApproved
		message = "You are now a member of " + communityName
	case res.Status == JoinStatusDeclined:
		notifType = NotificationCommunityDeclined
		message = "Your request to join " + communityName + " was declined"
	default:
		// Any other status (pending, banned, blocked) is not a resolution the
		// requester asked about, so stay quiet rather than inventing copy for it.
		return models.Notification{}, false
	}

	return models.Notification{
		ID:         primitive.NewObjectID().Hex(),
		SentFromID: res.ActorID,
		SentToID:   res.RequesterID,
		Type:       notifType,
		Message:    message,
		Data1:      res.CommunityID,
		Data2:      res.CommunityName,
		Data3:      res.DepartmentID,
		Data4:      res.DepartmentName,
		Seen:       false,
		CreatedAt:  time.Now(),
	}, true
}

// notifyJoinResolved stores and delivers the notification for a resolved join
// request. It is best-effort throughout: a member has already been approved or
// declined in the database by the time this runs, and failing to tell them must
// never turn a successful resolution into an error response.
func notifyJoinResolved(
	ctx context.Context,
	udb databases.UserDatabase,
	ptdb databases.PushTokenDatabase,
	updb databases.UserPreferencesDatabase,
	res JoinResolution,
) {
	notification, ok := BuildJoinResolvedNotification(res)
	if !ok {
		return
	}

	requesterID, err := primitive.ObjectIDFromHex(res.RequesterID)
	if err != nil {
		zap.S().Warnw("join resolution: requester id is not an object id",
			"user_id", res.RequesterID, "community_id", res.CommunityID, "error", err)
		return
	}

	// $push creates the array when the field is missing or null, so unlike
	// AddNotificationHandler this needs no separate initialize branch.
	filter := bson.M{"_id": requesterID}
	update := bson.M{"$push": bson.M{"user.notifications": notification}}
	if _, err := udb.UpdateOne(ctx, filter, update); err != nil {
		zap.S().Errorw("join resolution: failed to store notification",
			"user_id", res.RequesterID, "community_id", res.CommunityID,
			"type", notification.Type, "error", err)
		return
	}

	sendNotificationToUser(res.RequesterID, joinResolvedWSPayload(notification))
	go sendNotificationPush(ptdb, updb, res.RequesterID, notification, "")
}

// joinResolvedWSPayload mirrors the enriched shape AddNotificationHandler puts
// on the socket. These notifications come from the system rather than a person,
// so the sender fields are present but blank: the clients read them
// unconditionally, and omitting the keys renders "undefined" in the toast.
func joinResolvedWSPayload(n models.Notification) map[string]interface{} {
	return map[string]interface{}{
		"_id":              n.ID,
		"sentFromID":       n.SentFromID,
		"sentToID":         n.SentToID,
		"type":             n.Type,
		"message":          n.Message,
		"data1":            n.Data1,
		"data2":            n.Data2,
		"data3":            n.Data3,
		"data4":            n.Data4,
		"seen":             n.Seen,
		"createdAt":        n.CreatedAt,
		"senderUsername":   "",
		"senderProfilePic": "",
	}
}
