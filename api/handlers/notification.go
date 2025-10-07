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
	clients map[string]*websocket.Conn
	mutex   sync.Mutex
}

var hub = &NotificationHub{
	clients: make(map[string]*websocket.Conn),
	mutex:   sync.Mutex{},
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

	// Register client
	hub.mutex.Lock()
	hub.clients[userId] = conn
	hub.mutex.Unlock()
	log.Printf("User %s connected to /ws/notifications", userId)

	// Handle disconnect
	conn.SetCloseHandler(func(code int, text string) error {
		hub.mutex.Lock()
		delete(hub.clients, userId)
		hub.mutex.Unlock()
		log.Printf("User %s disconnected from /ws/notifications", userId)
		return nil
	})

	// Keep connection alive
	for {
		if _, _, err := conn.NextReader(); err != nil {
			conn.Close()
			break
		}
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
			delete(hub.clients, userId)
			hub.mutex.Unlock()
			conn.Close()
		}
	}
}

// Broadcast panic alert event to all connected users
func broadcastPanicAlertEvent(eventType string, data map[string]interface{}) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()

	log.Printf("Broadcasting panic alert event: %s with data: %+v to %d connected users", eventType, data, len(hub.clients))
	
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
			delete(hub.clients, userId)
			conn.Close()
		} else {
			log.Printf("Successfully sent panic alert event %s to user %s", eventType, userId)
		}
	}
}
