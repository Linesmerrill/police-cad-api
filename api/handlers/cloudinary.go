package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/cloudinary/cloudinary-go/api"
)

// CloudinaryHandler handles Cloudinary related requests
type CloudinaryHandler struct{}

// GenerateSignature generates a signature for Cloudinary uploads
func (c CloudinaryHandler) GenerateSignature(w http.ResponseWriter, r *http.Request) {
	uploadPreset := os.Getenv("CLOUDINARY_UPLOAD_PRESET")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	timestamp := time.Now().Unix()

	// Create the parameters to sign
	paramsToSign := url.Values{
		"timestamp":     {strconv.FormatInt(timestamp, 10)},
		"upload_preset": {uploadPreset},
	}

	// Generate the signature using Cloudinary SDK
	signature, err := api.SignParameters(paramsToSign, apiSecret)
	if err != nil {
		http.Error(w, "failed to generate signature", http.StatusInternalServerError)
		return
	}

	// Respond with the timestamp and signature
	response := map[string]string{
		"timestamp": strconv.FormatInt(timestamp, 10),
		"signature": signature,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
