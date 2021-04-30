package config_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	os.Setenv("DB_URI", "mongodb://127.0.0.1:27017")
	os.Setenv("DB_NAME", "test")
	conf := config.New()

	assert.NotEmpty(t, conf)
}

func TestErrorStatus(t *testing.T) {

	config.ErrorStatus("error it borked", http.StatusBadRequest, httptest.NewRecorder(), errors.New("bad request"))
	assert.True(t, true)
}
