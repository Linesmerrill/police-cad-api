package handlers

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"
)

// CloudinaryHandler handles Cloudinary related requests
type CloudinaryHandler struct{}

// GenerateSignature generates a signature for Cloudinary uploads
func (c CloudinaryHandler) GenerateSignature(w http.ResponseWriter, r *http.Request) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	uploadPreset := os.Getenv("CLOUDINARY_UPLOAD_PRESET")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")

	// Create the signature
	h := hmac.New(sha1.New, []byte(apiSecret))
	h.Write([]byte("timestamp=" + timestamp + "&upload_preset=" + uploadPreset))
	signature := hex.EncodeToString(h.Sum(nil))

	// Respond with the timestamp and signature
	response := map[string]string{
		"timestamp": timestamp,
		"signature": signature,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
