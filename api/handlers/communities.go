package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/gorilla/mux"

	"github.com/linesmerrill/police-cad-api/models"
)

// CommunityHandler returns a community given a communityID
func (a *App) CommunityHandler(w http.ResponseWriter, r *http.Request) {
	commID := mux.Vars(r)["community_id"]

	zap.S().Debugf("community_id: %v", commID)

	comm := models.Community{ID: commID}
	dbResp, err := comm.GetCommunity(context.Background(), a.DB)
	if err != nil {
		zap.S().With(err).Error("failed to get community by ID")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(fmt.Sprintf(`{"response": "failed to get community by ID, %v"}`, err)))
		return
	}

	b, err := json.Marshal(dbResp)
	if err != nil {
		zap.S().With(err).Error("failed to marshal response")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(fmt.Sprintf(`{"error": "failed to marshal response, %v"}`, err)))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
