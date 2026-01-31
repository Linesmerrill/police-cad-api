package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	expoPushURL    = "https://exp.host/--/api/v2/push/send"
	expoBatchLimit = 100
)

// ExpoPushMessage represents a single push notification message for the Expo push API
type ExpoPushMessage struct {
	To        string                 `json:"to"`
	Title     string                 `json:"title,omitempty"`
	Body      string                 `json:"body,omitempty"`
	Sound     string                 `json:"sound,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Priority  string                 `json:"priority,omitempty"`
	ChannelID string                 `json:"channelId,omitempty"`
}

// SendExpoPushNotifications sends push notifications to a list of Expo push tokens.
// Tokens are batched in groups of 100 per the Expo API limit.
func SendExpoPushNotifications(tokens []string, title string, body string, data map[string]interface{}) error {
	if len(tokens) == 0 {
		return nil
	}

	// Build messages
	var messages []ExpoPushMessage
	for _, token := range tokens {
		messages = append(messages, ExpoPushMessage{
			To:        token,
			Title:     title,
			Body:      body,
			Sound:     "default",
			Data:      data,
			Priority:  "high",
			ChannelID: "default",
		})
	}

	// Send in batches
	for i := 0; i < len(messages); i += expoBatchLimit {
		end := i + expoBatchLimit
		if end > len(messages) {
			end = len(messages)
		}
		batch := messages[i:end]

		if err := sendExpoBatch(batch); err != nil {
			zap.S().Errorf("Failed to send Expo push batch (tokens %d-%d): %v", i, end-1, err)
			// Continue with remaining batches even if one fails
		}
	}

	return nil
}

func sendExpoBatch(messages []ExpoPushMessage) error {
	jsonData, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal push messages: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", expoPushURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create push request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send push request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expo push API returned status %d", resp.StatusCode)
	}

	zap.S().Infof("Successfully sent %d push notification(s) via Expo", len(messages))
	return nil
}
