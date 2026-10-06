package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// allowCivilianRecordDeletionKey is the community PATCH key for
// CommunityDetails.AllowCivilianRecordDeletion.
const allowCivilianRecordDeletionKey = "allowCivilianRecordDeletion"

// recordDeletionRestrictedMessage is shown to a player who tries to delete a
// record on their own character while the community has turned that off.
const recordDeletionRestrictedMessage = "This community doesn't allow players to delete records on their own characters. An admin or someone with Manage Records can remove it, or change this in the community's General Settings."

// civilianRecordDeletionBlocked is the whole rule, kept free of I/O so it can
// be tested directly. A delete is refused only when every one of these holds:
//
//   - the community has explicitly turned off "Allow civilians to delete their
//     own records" (unset means allowed);
//   - the requester is the player who owns the character the record is on;
//   - the requester is not the community owner and holds neither
//     "administrator" nor "manage records".
//
// Everyone else (officers, dispatchers, other players) keeps whatever access
// they had before; this setting only takes something away from the
// character's owner.
func civilianRecordDeletionBlocked(community *models.Community, civilianOwnerID, requesterID string) bool {
	if community == nil || requesterID == "" || civilianOwnerID == "" {
		return false
	}
	if requesterID != civilianOwnerID {
		return false
	}
	if community.Details.CivilianRecordDeletionAllowed() {
		return false
	}
	return !userHasCommunityPermission(community, requesterID, "manage records")
}

// recordDeletionRequester identifies who is deleting. The bearer token is
// preferred; the website's browser JS has no token and sends ?userId=, which
// is forgeable. That is acceptable here because the identity can only ever
// cause a refusal: claiming to be someone else, or sending nothing, gets
// exactly the access an anonymous request already has, never more.
func recordDeletionRequester(r *http.Request) string {
	return resolveActorFromRequest(r)
}

// findRecordCommunity resolves the community a record belongs to, trying each
// community ID hint in order and then the issuing department. Returns nil when
// none resolve; the caller treats that as "no setting", i.e. allowed.
func findRecordCommunity(ctx context.Context, commDB databases.CommunityDatabase, departmentID string, communityIDs ...string) *models.Community {
	if commDB == nil {
		return nil
	}
	for _, id := range communityIDs {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			continue
		}
		community, err := commDB.FindOne(ctx, bson.M{"_id": oid})
		if err == nil && community != nil {
			return community
		}
	}
	if deptID, err := primitive.ObjectIDFromHex(departmentID); err == nil {
		community, ferr := commDB.FindOne(ctx, bson.M{"community.departments._id": deptID})
		if ferr == nil && community != nil {
			return community
		}
	}
	return nil
}

// enforceCivilianRecordDeletion applies civilianRecordDeletionBlocked to a
// record on civ and writes the 403 when it applies. Returns true when the
// response has been written and the caller must stop.
//
// Lookups that fail open (no community found) leave the delete allowed: the
// setting can only remove access, so not finding it means it isn't on.
func enforceCivilianRecordDeletion(ctx context.Context, w http.ResponseWriter, commDB databases.CommunityDatabase, civ *models.Civilian, requesterID, departmentID string, communityIDs ...string) bool {
	if civ == nil || requesterID == "" || civ.Details.UserID != requesterID {
		// Only the character's owner can ever be refused, so skip the
		// community lookup for everyone else.
		return false
	}
	hints := append(append([]string{}, communityIDs...), civ.Details.ActiveCommunityID)
	community := findRecordCommunity(ctx, commDB, departmentID, hints...)
	if community == nil {
		zap.S().Debugw("record deletion: no community resolved, allowing", "civilianID", civ.ID.Hex(), "departmentID", departmentID)
		return false
	}
	if !civilianRecordDeletionBlocked(community, civ.Details.UserID, requesterID) {
		return false
	}
	writeRecordDeletionRestricted(w)
	return true
}

func writeRecordDeletionRestricted(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "record_deletion_restricted",
		"message": recordDeletionRestrictedMessage,
	})
}

// validateAllowCivilianRecordDeletionPatch checks a community PATCH body for
// the record-deletion setting. gated is true when the body touches it, in
// which case the caller must authorize the actor. Only the exact key with a
// boolean value is accepted; a dotted path or any other shape is refused so
// the stored value always decodes as *bool.
func validateAllowCivilianRecordDeletionPatch(req map[string]interface{}) (gated bool, err error) {
	for key, value := range req {
		root := key
		if i := strings.Index(key, "."); i >= 0 {
			root = key[:i]
		}
		if root != allowCivilianRecordDeletionKey {
			continue
		}
		if key != allowCivilianRecordDeletionKey {
			return true, fmt.Errorf("%s must be set as a whole", allowCivilianRecordDeletionKey)
		}
		if _, ok := value.(bool); !ok {
			return true, fmt.Errorf("invalid %s: expected boolean", allowCivilianRecordDeletionKey)
		}
		gated = true
	}
	return gated, nil
}
