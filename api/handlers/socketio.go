package handlers

import (
	"log"

	socketio "github.com/googollee/go-socket.io"
	"github.com/googollee/go-socket.io/engineio"
	"github.com/googollee/go-socket.io/engineio/transport"
	"github.com/googollee/go-socket.io/engineio/transport/polling"
	"github.com/googollee/go-socket.io/engineio/transport/websocket"
)

var server *socketio.Server

// InitializeSocketIO initializes the Socket.IO server
func InitializeSocketIO() *socketio.Server {
	server = socketio.NewServer(&engineio.Options{
		Transports: []transport.Transport{
			polling.Default,
			websocket.Default,
		},
	})

	server.OnConnect("/", func(s socketio.Conn) error {
		s.SetContext("")
		log.Println("Socket.IO client connected:", s.ID())
		return nil
	})

	server.OnError("/", func(s socketio.Conn, e error) {
		log.Println("Socket.IO error:", e)
	})

	server.OnDisconnect("/", func(s socketio.Conn, reason string) {
		log.Println("Socket.IO client disconnected:", s.ID(), "reason:", reason)
	})

	// Handle panic button events
	server.OnEvent("/", "panic_button_pressed", func(s socketio.Conn, msg map[string]interface{}) {
		log.Println("Panic button pressed:", msg)
		// Broadcast to all clients in the community
		server.BroadcastToRoom("/", "", "panic_button_pressed", msg)
	})

	server.OnEvent("/", "join_community", func(s socketio.Conn, msg map[string]interface{}) {
		communityId, ok := msg["communityId"].(string)
		if ok {
			s.Join(communityId)
			log.Println("Client joined community:", communityId)
		}
	})

	server.OnEvent("/", "leave_community", func(s socketio.Conn, msg map[string]interface{}) {
		communityId, ok := msg["communityId"].(string)
		if ok {
			s.Leave(communityId)
			log.Println("Client left community:", communityId)
		}
	})

	go func() {
		if err := server.Serve(); err != nil {
			log.Fatalf("Socket.IO server error: %v", err)
		}
	}()

	return server
}

// GetSocketIOServer returns the Socket.IO server instance
func GetSocketIOServer() *socketio.Server {
	return server
}

// EmitPanicButtonCleared emits a panic_button_cleared event to all clients in a community
func EmitPanicButtonCleared(communityId string, userId string) {
	if server != nil {
		data := map[string]interface{}{
			"userId":      userId,
			"communityId": communityId,
		}
		server.BroadcastToRoom("/", communityId, "panic_button_cleared", data)
		log.Printf("Emitted panic_button_cleared to community %s for user %s", communityId, userId)
	}
}

// EmitPanicAlertCreated emits a panic_alert_created event to all clients in a community
func EmitPanicAlertCreated(communityId string, alertData map[string]interface{}) {
	if server != nil {
		server.BroadcastToRoom("/", communityId, "panic_alert_created", alertData)
		log.Printf("Emitted panic_alert_created to community %s", communityId)
	}
}

// EmitPanicAlertsUpdated emits a panic_alerts_updated event to all clients in a community
func EmitPanicAlertsUpdated(communityId string, alertsData map[string]interface{}) {
	if server != nil {
		server.BroadcastToRoom("/", communityId, "panic_alerts_updated", alertsData)
		log.Printf("Emitted panic_alerts_updated to community %s", communityId)
	}
}
