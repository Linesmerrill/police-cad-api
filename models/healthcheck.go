package models

// HealthCheckResponse returns the health check response duh
type HealthCheckResponse struct {
	Alive bool `json:"alive"`
}
