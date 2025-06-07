package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"go.mongodb.org/mongo-driver/bson"
)

// InviteCode exported for testing purposes
type InviteCode struct {
	DB databases.InviteCodeDatabase
}

// InviteCodeByCodeHandler returns an invite code by its code
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(codeData)
}
