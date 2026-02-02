package handlers

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Adjust CORS as needed, e.g., check r.Header.Get("Origin")
	},
}

// Store connected users (userId -> *websocket.Conn)
type NotificationHub struct {
	clients          map[string]*websocket.Conn
	userCommunities  map[string]string            // userId -> communityId (tracks which community each user is in)
	communityClients map[string]map[string]bool   // communityId -> set of userIds
	mutex            sync.Mutex
}

var hub = &NotificationHub{
	clients:          make(map[string]*websocket.Conn),
	userCommunities:  make(map[string]string),
	communityClients: make(map[string]map[string]bool),
	mutex:            sync.Mutex{},
}

// HandleNotificationsWebSocket WebSocket handler for notifications
func HandleNotificationsWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Get userId from query param (replace with JWT/auth middleware in production)
	userId := r.URL.Query().Get("userId")
	if userId == "" {
		conn.Close()
		return
	}

	// Optional: community ID for scoped broadcasting
	communityId := r.URL.Query().Get("communityId")

	// Register client
	hub.mutex.Lock()
	hub.clients[userId] = conn
	if communityId != "" {
		hub.userCommunities[userId] = communityId
		if hub.communityClients[communityId] == nil {
			hub.communityClients[communityId] = make(map[string]bool)
		}
		hub.communityClients[communityId][userId] = true
	}
	hub.mutex.Unlock()
	log.Printf("User %s connected to /ws/notifications (community: %s)", userId, communityId)

	// Handle disconnect
	conn.SetCloseHandler(func(code int, text string) error {
		hub.mutex.Lock()
		removeUserFromHub(userId)
		hub.mutex.Unlock()
		log.Printf("User %s disconnected from /ws/notifications", userId)
		return nil
	})

	// Keep connection alive
	for {
		if _, _, err := conn.NextReader(); err != nil {
			hub.mutex.Lock()
			removeUserFromHub(userId)
			hub.mutex.Unlock()
			conn.Close()
			break
		}
	}
}

// removeUserFromHub removes a user from all hub tracking maps. Must be called with hub.mutex held.
func removeUserFromHub(userId string) {
	delete(hub.clients, userId)
	if cid, ok := hub.userCommunities[userId]; ok {
		if hub.communityClients[cid] != nil {
			delete(hub.communityClients[cid], userId)
			if len(hub.communityClients[cid]) == 0 {
				delete(hub.communityClients, cid)
			}
		}
		delete(hub.userCommunities, userId)
	}
}

// Broadcast notification to a user
func sendNotificationToUser(userId string, notification interface{}) {
	hub.mutex.Lock()
	conn, exists := hub.clients[userId]
	hub.mutex.Unlock()

	if exists {
		err := conn.WriteJSON(map[string]interface{}{
			"event": "new_notification",
			"data":  notification,
		})
		if err != nil {
			log.Printf("Error sending notification to user %s: %v", userId, err)
			hub.mutex.Lock()
			removeUserFromHub(userId)
			hub.mutex.Unlock()
			conn.Close()
		}
	}
}

// broadcastPanicAlertEvent broadcasts a panic alert event.
// If communityId is provided, only users in that community receive the event.
// If communityId is empty, all connected users receive it (backward compatibility).
func broadcastPanicAlertEvent(eventType string, data map[string]interface{}) {
	communityId, _ := data["communityId"].(string)

	hub.mutex.Lock()
	defer hub.mutex.Unlock()

	if communityId != "" {
		// Community-scoped broadcast
		userIds := hub.communityClients[communityId]
		log.Printf("Broadcasting panic alert event: %s to %d users in community %s", eventType, len(userIds), communityId)

		if len(userIds) == 0 {
			log.Printf("WARNING: No websocket clients in community %s for event %s", communityId, eventType)
			return
		}

		for userId := range userIds {
			conn, exists := hub.clients[userId]
			if !exists {
				continue
			}
			err := conn.WriteJSON(map[string]interface{}{
				"event": eventType,
				"data":  data,
			})
			if err != nil {
				log.Printf("Error broadcasting panic alert event to user %s: %v", userId, err)
				removeUserFromHub(userId)
				conn.Close()
			} else {
				log.Printf("Successfully sent panic alert event %s to user %s (community: %s)", eventType, userId, communityId)
			}
		}
	} else {
		// Global broadcast (backward compatibility)
		log.Printf("Broadcasting panic alert event: %s to %d connected users (global)", eventType, len(hub.clients))

		if len(hub.clients) == 0 {
			log.Printf("WARNING: No websocket clients connected! Panic alert event %s will not be delivered", eventType)
			return
		}

		for userId, conn := range hub.clients {
			err := conn.WriteJSON(map[string]interface{}{
				"event": eventType,
				"data":  data,
			})
			if err != nil {
				log.Printf("Error broadcasting panic alert event to user %s: %v", userId, err)
				removeUserFromHub(userId)
				conn.Close()
			} else {
				log.Printf("Successfully sent panic alert event %s to user %s", eventType, userId)
			}
		}
	}
}
