package handlers

import "net/http"

func CommunitiesHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"test": "123"}`))
}
