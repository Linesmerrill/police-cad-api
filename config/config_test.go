package config

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	os.Setenv("DB_URI", "mongodb://127.0.0.1:27017")
	os.Setenv("DB_NAME", "test")
	conf := New()

	assert.NotEmpty(t, conf)
}

func TestErrorStatus(t *testing.T) {

	ErrorStatus("error it borked", http.StatusBadRequest, httptest.NewRecorder(), errors.New("bad request"))
	assert.True(t, true)
}

func TestSetLoggerSetsDevelopmentLogger(t *testing.T) {
	l, err := setLogger("development")
	assert.NoError(t, err)
	assert.True(t, l.Core().Enabled(1))
}

func TestSetLoggerSetsProductionLogger(t *testing.T) {
	l, err := setLogger("production")
	assert.NoError(t, err)
	assert.True(t, l.Core().Enabled(2))
}

func TestSetLoggerSetsLocalLogger(t *testing.T) {
	l, err := setLogger("local")
	assert.NoError(t, err)
	assert.True(t, l.Core().Enabled(0))
}
