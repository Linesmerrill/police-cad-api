package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// The heartbeat endpoint is the one thing standing between "the bot is up" and
// anybody being able to assert it. These tests pin that it fails closed: the
// only way through is the configured secret, and with no secret configured
// nothing gets through at all.

const testHeartbeatToken = "s3cret-heartbeat-token"

func TestBotHeartbeat_NoTokenConfigured_IsRefused(t *testing.T) {
	// Deliberately NOT setting BOT_HEARTBEAT_TOKEN.
	t.Setenv(botHeartbeatTokenEnv, "")

	// A strict mock: any call to the store fails the test, which is the real
	// assertion here. An unconfigured API must not record anything.
	mockDB := mocks.NewBotHealthDatabase(t)

	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(`{"shardId":0}`))
	req.Header.Set(botHeartbeatTokenHeader, "anything-at-all")
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"with no secret configured the endpoint must fail closed, never accept the write")
}

func TestBotHeartbeat_MissingToken_IsUnauthorized(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)
	mockDB := mocks.NewBotHealthDatabase(t)

	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestBotHeartbeat_WrongToken_IsUnauthorized(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)
	mockDB := mocks.NewBotHealthDatabase(t)

	for _, wrong := range []string{
		"wrong-token",
		// A prefix of the real token: the compare must be on the whole value.
		testHeartbeatToken[:len(testHeartbeatToken)-1],
		testHeartbeatToken + "x",
		strings.ToUpper(testHeartbeatToken),
	} {
		req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(`{}`))
		req.Header.Set(botHeartbeatTokenHeader, wrong)
		w := httptest.NewRecorder()
		BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code, "token %q must not be accepted", wrong)
		assert.NotContains(t, w.Body.String(), testHeartbeatToken,
			"the response must never echo the expected token")
		assert.NotContains(t, w.Body.String(), wrong,
			"the response must never echo what was sent either")
	}
}

func TestBotHeartbeat_CorrectToken_IsRecorded(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)

	mockDB := &mocks.BotHealthDatabase{}
	var got models.BotHeartbeat
	var gotAt time.Time
	mockDB.On("RecordHeartbeat", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(models.BotHeartbeat)
			gotAt = args.Get(2).(time.Time)
		}).
		Return(nil)

	body := `{"shardId":1,"guildCount":42,"version":"4.0.8","uptimeSeconds":3600,"somethingNew":"ignored"}`
	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(body))
	req.Header.Set(botHeartbeatTokenHeader, testHeartbeatToken)
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDB.AssertExpectations(t)
	assert.Equal(t, models.BotHeartbeat{ShardID: 1, GuildCount: 42, Version: "4.0.8", UptimeSeconds: 3600}, got,
		"an unknown field must be ignored rather than rejected, so a newer bot keeps heartbeating")
	assert.WithinDuration(t, time.Now(), gotAt, time.Minute,
		"the receive time must come from the API's clock, not the bot's")
	assert.NotContains(t, w.Body.String(), testHeartbeatToken)
}

func TestBotHeartbeat_EmptyBody_IsAccepted(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)

	mockDB := &mocks.BotHealthDatabase{}
	mockDB.On("RecordHeartbeat", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", nil)
	req.Header.Set(botHeartbeatTokenHeader, testHeartbeatToken)
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "liveness is the signal; the decorations are optional")
	mockDB.AssertExpectations(t)
}

func TestBotHeartbeat_MalformedBody_IsRejected(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)
	mockDB := mocks.NewBotHealthDatabase(t)

	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(`not json`))
	req.Header.Set(botHeartbeatTokenHeader, testHeartbeatToken)
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBotHeartbeat_OutOfRangeValues_AreClamped(t *testing.T) {
	t.Setenv(botHeartbeatTokenEnv, testHeartbeatToken)

	mockDB := &mocks.BotHealthDatabase{}
	var got models.BotHeartbeat
	mockDB.On("RecordHeartbeat", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(models.BotHeartbeat) }).
		Return(nil)

	body := `{"shardId":-5,"guildCount":99999999,"version":"` + strings.Repeat("v", 90) + `","uptimeSeconds":-1}`
	req := httptest.NewRequest(http.MethodPost, "/health/discord-bot/heartbeat", strings.NewReader(body))
	req.Header.Set(botHeartbeatTokenHeader, testHeartbeatToken)
	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.HeartbeatHandler(w, req)

	// Clamped rather than refused: these fields only decorate an alert, and
	// refusing the write over one would turn a cosmetic bot bug into a false
	// outage.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 0, got.ShardID)
	assert.Equal(t, models.BotHeartbeatMaxGuildCount, got.GuildCount)
	assert.Equal(t, int64(0), got.UptimeSeconds)
	assert.Len(t, got.Version, models.BotHeartbeatMaxVersionLen)
}

func TestBotHeartbeat_VersionStripsControlCharacters(t *testing.T) {
	assert.Equal(t, "4.0.8", sanitizeBotVersion("4.0.8\n\x00"))
	assert.Equal(t, "", sanitizeBotVersion("\x1b\x07"))
}

// --- public status endpoint ---

func TestBotStatus_FreshHeartbeat_IsUp(t *testing.T) {
	mockDB := &mocks.BotHealthDatabase{}
	last := time.Now().UTC().Add(-30 * time.Second)
	mockDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:              models.BotHealthDocID,
		LastHeartbeatAt: last,
		ShardID:         0,
		GuildCount:      137,
		BotVersion:      "4.0.8",
		UptimeSeconds:   900,
	}, nil)

	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/health/discord-bot", nil))

	assert.Equal(t, http.StatusOK, w.Code)

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, models.BotHealthStateUp, got["status"])
	assert.Equal(t, last.Format(time.RFC3339), got["lastHeartbeat"])
	assert.Equal(t, float64(137), got["guildCount"])
	assert.Equal(t, "4.0.8", got["version"])
	assert.Equal(t, float64(180), got["staleAfterSeconds"])
}

func TestBotStatus_StaleHeartbeat_IsDown(t *testing.T) {
	mockDB := &mocks.BotHealthDatabase{}
	last := time.Now().UTC().Add(-models.BotHeartbeatStaleAfter - time.Second)
	mockDB.On("Get", mock.Anything).Return(&models.BotHealth{
		ID:              models.BotHealthDocID,
		LastHeartbeatAt: last,
		GuildCount:      137,
	}, nil)

	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/health/discord-bot", nil))

	// 503 so an uptime monitor reads it as down with no extra configuration.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, models.BotHealthStateDown, got["status"])
	assert.Equal(t, last.Format(time.RFC3339), got["lastHeartbeat"])
}

func TestBotStatus_NeverHeartbeated_IsDown(t *testing.T) {
	mockDB := &mocks.BotHealthDatabase{}
	mockDB.On("Get", mock.Anything).Return(nil, nil)

	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/health/discord-bot", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var got map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, models.BotHealthStateDown, got["status"],
		"no evidence of life must not read as green")
	assert.Equal(t, "", got["lastHeartbeat"])
	assert.NotContains(t, got, "shardId", "absent fields must be omitted, not reported as zero")
}

func TestBotStatus_StoreError_IsDown(t *testing.T) {
	mockDB := &mocks.BotHealthDatabase{}
	mockDB.On("Get", mock.Anything).Return(nil, errors.New("mongo is unreachable"))

	w := httptest.NewRecorder()
	BotHealth{DB: mockDB}.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/health/discord-bot", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotContains(t, w.Body.String(), "mongo is unreachable",
		"the public endpoint must not narrate our internals")
}
