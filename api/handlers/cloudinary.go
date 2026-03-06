package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/cloudinary/cloudinary-go/v2/api"
)

// CloudinaryHandler handles Cloudinary related requests
type CloudinaryHandler struct{}

// GenerateSignature generates a signature for Cloudinary uploads
func (c CloudinaryHandler) GenerateSignature(w http.ResponseWriter, r *http.Request) {
	uploadPreset := os.Getenv("CLOUDINARY_UPLOAD_PRESET")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Create the parameters to sign
	paramsToSign := url.Values{
		"timestamp":     {timestamp},
		"upload_preset": {uploadPreset},
	}

	// Generate the signature using Cloudinary SDK
	signature, err := api.SignParameters(paramsToSign, apiSecret)
	if err != nil {
		http.Error(w, "failed to generate signature: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Respond with the timestamp and signature
	response := map[string]string{
		"timestamp": timestamp,
		"signature": signature,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
