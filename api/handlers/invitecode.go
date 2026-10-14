package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
)

// InviteCode exported for testing purposes
type InviteCode struct {
	DB  databases.InviteCodeDatabase
	CDB databases.CommunityDatabase
}

// InviteCodeByCodeHandler looks up an invite code for someone holding a link.
//
// Anyone with the code can ask, so the answer is only what a prospective joiner
// needs in order to decide whether to use it: which community it leads to,
// whether it still works, and when it runs out. How often it has been used, how
// many uses are left and who created it are the community's business, not a
// visitor's.
func (i InviteCode) InviteCodeByCodeHandler(w http.ResponseWriter, r *http.Request) {
	inviteCode := r.URL.Query().Get("code")
	if inviteCode == "" {
		config.ErrorStatus("invite code is required", http.StatusBadRequest, w, nil)
		return
	}
	filter := bson.M{"code": inviteCode}
	codeData, err := i.DB.FindOne(context.Background(), filter)
	if err != nil {
		config.ErrorStatus("failed to find invite code", http.StatusNotFound, w, err)
		return
	}

	communityObjID, err := primitive.ObjectIDFromHex(codeData.CommunityID)
	if err != nil {
		config.InfoStatus("failed to find invite code", http.StatusNotFound, w, err)
		return
	}
	community, err := i.CDB.FindOne(context.Background(), bson.M{"_id": communityObjID})
	if err != nil {
		// With no community behind it there is nothing to join, so the code is
		// as good as gone.
		config.InfoStatus("failed to find invite code", http.StatusNotFound, w, err)
		return
	}

	usable := !inviteCodeExpired(codeData, time.Now()) && inviteCodeHasUsesLeft(codeData)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":          codeData.Code,
		"communityId":   codeData.CommunityID,
		"communityName": community.Details.Name,
		"usable":        usable,
		"expiresAt":     codeData.ExpiresAt,
	})
}
