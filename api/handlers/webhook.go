package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
)

// notifyNodeServer posts a webhook to the Node server so it can fan out a
// realtime event to every tab in a community room over Socket.IO. This is the
// bridge that lets REST-originated writes (Command Bridge, classic dashboards,
// mobile app) all show up live on every other connected dashboard.
//
// The Node endpoint is a single catch-all (/internal/panic-broadcast — named
// for historical reasons) that routes on the `event` field. Each new event
// type needs a matching branch in the Node route.
//
// Call as a goroutine — it blocks on an HTTP round-trip and has no bearing on
// the user's write succeeding.
func notifyNodeServer(eventType string, communityID string, data map[string]interface{}) {
	nodeServerURL := os.Getenv("NODE_SERVER_WEBHOOK_URL")
	apiKey := os.Getenv("NODE_SERVER_API_KEY")

	if nodeServerURL == "" {
		// Not configured in this environment — skip silently. Realtime is a
		// progressive enhancement; writes still succeed against Mongo.
		return
	}

	payload := map[string]interface{}{
		"event":       eventType,
		"communityId": communityID,
		"data":        data,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		zap.S().Errorf("notifyNodeServer: marshal failed for %s: %v", eventType, err)
		return
	}

	req, err := http.NewRequest("POST", nodeServerURL, bytes.NewBuffer(jsonData))
	if err != nil {
		zap.S().Errorf("notifyNodeServer: build request failed for %s: %v", eventType, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-API-Key", apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		zap.S().Errorf("notifyNodeServer: POST failed for %s: %v", eventType, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		zap.S().Warnf("notifyNodeServer: %s returned status %d", eventType, resp.StatusCode)
	}
}
