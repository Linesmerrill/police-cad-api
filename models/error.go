package models

// ErrorMessageResponse returns the error message response struct
type ErrorMessageResponse struct {
	Response MessageError `json:"response"`
}

// MessageError contains the inner details for the error message response
type MessageError struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}
