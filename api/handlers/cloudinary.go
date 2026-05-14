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

// GenerateSignature generates a signature for Cloudinary uploads.
// Accepts optional parameters in the request body (e.g. "folder", "upload_preset")
// that will be included in the signed string so Cloudinary can verify them.
//
// Callers may pass "upload_preset" in the body to override the server default.
// This is how the community-map uploader opts into a full-resolution preset
// while everything else keeps the avatar/evidence-friendly default.
func (c CloudinaryHandler) GenerateSignature(w http.ResponseWriter, r *http.Request) {
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Decode optional extra parameters from request body
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body) // OK if empty/nil

	// Pick the upload preset: explicit body override, else server default.
	uploadPreset := os.Getenv("CLOUDINARY_UPLOAD_PRESET")
	if v, ok := body["upload_preset"]; ok && v != "" {
		uploadPreset = v
	}

	// Create the parameters to sign
	paramsToSign := url.Values{
		"timestamp":     {timestamp},
		"upload_preset": {uploadPreset},
	}

	// Include any extra signable parameters from the request
	signableParams := []string{"folder", "public_id", "resource_type"}
	for _, key := range signableParams {
		if val, ok := body[key]; ok && val != "" {
			paramsToSign.Set(key, val)
		}
	}

	// Generate the signature using Cloudinary SDK
	signature, err := api.SignParameters(paramsToSign, apiSecret)
	if err != nil {
		http.Error(w, "failed to generate signature: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Echo the preset back so the client uses the exact value we signed —
	// otherwise a stale window.CLOUDINARY_UPLOAD_PRESET on the page would
	// mismatch the signature and Cloudinary would reject the upload.
	response := map[string]string{
		"timestamp":     timestamp,
		"signature":     signature,
		"upload_preset": uploadPreset,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
