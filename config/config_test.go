package config

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	os.Setenv("DB_URI", "mongodb://127.0.0.1:27017")
	os.Setenv("DB_NAME", "test")
	conf := New()

	assert.NotEmpty(t, conf)
}

func TestErrorStatus(t *testing.T) {
	// The log level ErrorStatus chooses (Warn for 4xx/not-found, Error for 5xx)
	// must not change the HTTP status or body it returns to the caller.
	t.Run("client error still returns status and body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ErrorStatus("error it borked", http.StatusBadRequest, rec, errors.New("bad request"))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "error it borked")
	})

	t.Run("not found returns status and body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ErrorStatus("user not found", http.StatusNotFound, rec, mongo.ErrNoDocuments)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "user not found")
	})

	t.Run("server fault returns status and body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ErrorStatus("boom", http.StatusInternalServerError, rec, errors.New("db down"))
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "boom")
	})
}

// rawMongoLeak mimics the server-side detail a real Mongo driver error carries:
// the database name, collection, and index. None of it may reach a client.
const rawMongoLeak = `heroku_jfpckj4d0.formTemplates index: formTemplate_community_slug_unique`

func TestSafeClientErrorScrubsDuplicateKey(t *testing.T) {
	dupErr := mongo.WriteException{
		WriteErrors: mongo.WriteErrors{{
			Code:    11000,
			Message: "E11000 duplicate key error collection: " + rawMongoLeak,
		}},
	}

	got := safeClientError(dupErr)

	assert.Equal(t, "resource already exists", got)
	assert.NotContains(t, got, "heroku_")
	assert.NotContains(t, got, "formTemplates")
}

func TestSafeClientErrorScrubsGenericMongoError(t *testing.T) {
	cmdErr := mongo.CommandError{Code: 26, Message: "ns not found: " + rawMongoLeak}

	got := safeClientError(cmdErr)

	assert.Equal(t, "a database error occurred", got)
	assert.False(t, strings.Contains(got, "heroku_"))
}

func TestSafeClientErrorPassesThroughAppErrors(t *testing.T) {
	got := safeClientError(fmt.Errorf("slug is required"))
	assert.Equal(t, "slug is required", got)
}

func TestSafeClientErrorNil(t *testing.T) {
	assert.Equal(t, "", safeClientError(nil))
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

func TestSetLoggerDefaultsToProductionOnUnknownEnv(t *testing.T) {
	l, err := setLogger("asdf")
	// An unset/unrecognized LOG_LEVEL must fail closed to the production logger
	// (never debug), while still surfacing the misconfiguration via an error.
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "defaulting to production logger")
	assert.True(t, l.Core().Enabled(2))   // ErrorLevel enabled (production = Info+)
	assert.False(t, l.Core().Enabled(-1)) // DebugLevel must be OFF
}
