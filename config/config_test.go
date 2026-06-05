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

	ErrorStatus("error it borked", http.StatusBadRequest, httptest.NewRecorder(), errors.New("bad request"))
	assert.True(t, true)
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

func TestSetLoggerCannotFindEnv(t *testing.T) {
	l, err := setLogger("asdf")
	assert.Equal(t, err, fmt.Errorf("cannot find ENV var so defaulting to debug level logging"))
	assert.True(t, l.Core().Enabled(0))
}
