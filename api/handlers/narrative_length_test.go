package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/api/handlers/formdefaults"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// The narrative cap used to live only in the clients, so a direct call to the
// API - or a mobile build released before the cap existed - could store a
// report of any length. These tests pin the boundary: 1000 characters is
// accepted, 1001 is refused with the exact sentence the clients show, and a
// character that takes more than one byte still counts as one character.

const (
	narrativeLimitMessage = "Narrative can't be longer than 1000 characters."

	// One character, four bytes. A narrative of 1000 of these is 4000 bytes,
	// so a cap that counted bytes would refuse it at a quarter of the length.
	wideRune = "😀"
)

func narrativeOf(n int) string {
	return strings.Repeat("a", n)
}

// assertNarrativeRefused checks both halves of the contract: the status and the
// wording. The message is user-facing on every surface, so a drifted string is
// a regression even when the 400 is right.
func assertNarrativeRefused(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	var body models.ErrorMessageResponse
	assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, narrativeLimitMessage, body.Response.Message)
}

// ---- arrest reports ----

func arrestReportBody(narrative string) string {
	b, _ := json.Marshal(map[string]interface{}{
		"arrestReport": map[string]interface{}{"narrative": narrative},
	})
	return string(b)
}

func TestArrestReportCreate_AcceptsExactlyTheLimit(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	var stored models.ArrestReport
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Run(func(args mock.Arguments) { stored = args.Get(1).(models.ArrestReport) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.CreateArrestReportHandler(
		rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(arrestReportBody(narrativeOf(1000)))))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Len(t, stored.Details.Narrative, 1000)
}

func TestArrestReportCreate_RefusesOneOverTheLimit(t *testing.T) {
	// The write is wired up on purpose: a missing cap then fails the
	// AssertNotCalled below rather than panicking on an unexpected call.
	mockDB := &mocks.ArrestReportDatabase{}
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Return(&mocks.InsertOneResultHelper{}, nil)

	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.CreateArrestReportHandler(
		rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(arrestReportBody(narrativeOf(1001)))))

	assertNarrativeRefused(t, rr)
	// Refused before the write, not after it.
	mockDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

// Characters, not bytes: 1000 four-byte characters is a 1000-character
// narrative and must be accepted.
func TestArrestReportCreate_CountsMultiByteCharactersAsOne(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	var stored models.ArrestReport
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Run(func(args mock.Arguments) { stored = args.Get(1).(models.ArrestReport) }).
		Return(&mocks.InsertOneResultHelper{}, nil)

	wide := strings.Repeat(wideRune, 1000)
	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.CreateArrestReportHandler(
		rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(arrestReportBody(wide))))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Equal(t, 4000, len(stored.Details.Narrative), "the stored text really is multi-byte")
}

func TestArrestReportCreate_RefusesOneMultiByteCharacterOver(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	mockDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.ArrestReport")).
		Return(&mocks.InsertOneResultHelper{}, nil)

	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.CreateArrestReportHandler(
		rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(arrestReportBody(strings.Repeat(wideRune, 1001)))))

	assertNarrativeRefused(t, rr)
	mockDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
}

func arrestUpdateRequest(body string) *http.Request {
	r := httptest.NewRequest("PUT", "/", bytes.NewBufferString(body))
	return mux.SetURLVars(r, map[string]string{"arrest_report_id": primitive.NewObjectID().Hex()})
}

func TestArrestReportUpdate_AcceptsExactlyTheLimit(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	mockDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{"narrative": narrativeOf(1000)})
	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.UpdateArrestReportHandler(rr, arrestUpdateRequest(string(body)))

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestArrestReportUpdate_RefusesOneOverTheLimit(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	mockDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{"narrative": narrativeOf(1001)})
	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.UpdateArrestReportHandler(rr, arrestUpdateRequest(string(body)))

	assertNarrativeRefused(t, rr)
	mockDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// An edit that does not send the narrative leaves whatever is stored alone, so
// there is nothing to measure and nothing to refuse. A report saved before the
// cap existed stays editable.
func TestArrestReportUpdate_IgnoresABodyWithoutANarrative(t *testing.T) {
	mockDB := &mocks.ArrestReportDatabase{}
	mockDB.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	body, _ := json.Marshal(map[string]interface{}{"witnesses": strings.Repeat("w", 5000)})
	rr := httptest.NewRecorder()
	handlers.ArrestReport{DB: mockDB}.UpdateArrestReportHandler(rr, arrestUpdateRequest(string(body)))

	assert.Equal(t, http.StatusOK, rr.Code)
}

// ---- form submissions (the Reports editor) ----

// fakeFormSubmissionDB is a stand-in for the formSubmissions collection: it
// records what was written so a test can assert the write never happened.
type fakeFormSubmissionDB struct {
	existing *models.FormSubmission
	inserted []models.FormSubmission
	updates  int
}

func (f *fakeFormSubmissionDB) FindOne(_ context.Context, _ interface{}, _ ...*options.FindOneOptions) (*models.FormSubmission, error) {
	if f.existing == nil {
		return nil, mongo.ErrNoDocuments
	}
	return f.existing, nil
}

func (f *fakeFormSubmissionDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) ([]models.FormSubmission, error) {
	return nil, nil
}

func (f *fakeFormSubmissionDB) InsertOne(_ context.Context, doc interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	if sub, ok := doc.(models.FormSubmission); ok {
		f.inserted = append(f.inserted, sub)
	}
	return nil, nil
}

func (f *fakeFormSubmissionDB) DeleteOne(_ context.Context, _ interface{}, _ ...*options.DeleteOptions) error {
	return nil
}

func (f *fakeFormSubmissionDB) DeleteMany(_ context.Context, _ interface{}, _ ...*options.DeleteOptions) (int64, error) {
	return 0, nil
}

func (f *fakeFormSubmissionDB) UpdateOne(_ context.Context, _ interface{}, _ interface{}, _ ...*options.UpdateOptions) error {
	f.updates++
	return nil
}

func (f *fakeFormSubmissionDB) UpdateMany(_ context.Context, _ interface{}, _ interface{}, _ ...*options.UpdateOptions) (int64, error) {
	return 0, nil
}

func (f *fakeFormSubmissionDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return 0, nil
}

func (f *fakeFormSubmissionDB) Aggregate(_ context.Context, _ interface{}, _ ...*options.AggregateOptions) (*databases.MongoCursor, error) {
	return nil, nil
}

// fakeFormTemplateDB has no templates, so the handler resolves the built-in
// Incident Report - the template whose narrative field this cap is for.
type fakeFormTemplateDB struct{}

func (fakeFormTemplateDB) FindOne(_ context.Context, _ interface{}, _ ...*options.FindOneOptions) (*models.FormTemplate, error) {
	return nil, mongo.ErrNoDocuments
}

func (fakeFormTemplateDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) ([]models.FormTemplate, error) {
	return nil, nil
}

func (fakeFormTemplateDB) InsertOne(_ context.Context, _ interface{}, _ ...*options.InsertOneOptions) (databases.InsertOneResultHelper, error) {
	return nil, nil
}

func (fakeFormTemplateDB) DeleteOne(_ context.Context, _ interface{}, _ ...*options.DeleteOptions) error {
	return nil
}

func (fakeFormTemplateDB) UpdateOne(_ context.Context, _ interface{}, _ interface{}, _ ...*options.UpdateOptions) error {
	return nil
}

func (fakeFormTemplateDB) CountDocuments(_ context.Context, _ interface{}, _ ...*options.CountOptions) (int64, error) {
	return 0, nil
}

func formCreateBody(data map[string]interface{}) string {
	b, _ := json.Marshal(map[string]interface{}{
		"communityID":      primitive.NewObjectID().Hex(),
		"formTemplateSlug": formdefaults.IncidentReportSlug,
		// Supplied so the handler does not need a counter to allocate one.
		"reportNumber": "RR-2026-000001",
		"data":         data,
	})
	return string(b)
}

func postFormSubmission(body string) (*httptest.ResponseRecorder, *fakeFormSubmissionDB) {
	db := &fakeFormSubmissionDB{}
	h := handlers.FormSubmission{DB: db, TDB: fakeFormTemplateDB{}}
	rr := httptest.NewRecorder()
	h.CreateFormSubmissionHandler(rr, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))
	return rr, db
}

func TestFormSubmissionCreate_AcceptsExactlyTheLimit(t *testing.T) {
	rr, db := postFormSubmission(formCreateBody(map[string]interface{}{"narrative": narrativeOf(1000)}))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Len(t, db.inserted, 1)
}

func TestFormSubmissionCreate_RefusesOneOverTheLimit(t *testing.T) {
	rr, db := postFormSubmission(formCreateBody(map[string]interface{}{"narrative": narrativeOf(1001)}))

	assertNarrativeRefused(t, rr)
	assert.Empty(t, db.inserted, "an over-length report reached the collection")
}

func TestFormSubmissionCreate_CountsMultiByteCharactersAsOne(t *testing.T) {
	rr, db := postFormSubmission(formCreateBody(map[string]interface{}{
		"narrative": strings.Repeat(wideRune, 1000),
	}))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Len(t, db.inserted, 1)
}

// The cap is the narrative's alone. Every other field on a report, including
// the other textareas, keeps whatever limit it always had.
func TestFormSubmissionCreate_LeavesOtherFieldsAlone(t *testing.T) {
	rr, db := postFormSubmission(formCreateBody(map[string]interface{}{
		"narrative":   narrativeOf(10),
		"description": strings.Repeat("d", 5000),
		"witnesses":   strings.Repeat("w", 5000),
	}))

	assert.Equal(t, http.StatusCreated, rr.Code)
	assert.Len(t, db.inserted, 1)
}

// A non-string narrative is not this check's business: the field's own
// decoding owns that, and the cap must not turn it into a 400 about length.
func TestFormSubmissionCreate_IgnoresANonStringNarrative(t *testing.T) {
	rr, _ := postFormSubmission(formCreateBody(map[string]interface{}{"narrative": 42}))

	assert.NotEqual(t, http.StatusBadRequest, rr.Code)
}

func putFormSubmission(data map[string]interface{}) (*httptest.ResponseRecorder, *fakeFormSubmissionDB) {
	existing := &models.FormSubmission{ID: primitive.NewObjectID()}
	existing.Details = models.FormSubmissionDetails{Status: "draft"}
	db := &fakeFormSubmissionDB{existing: existing}
	h := handlers.FormSubmission{DB: db, TDB: fakeFormTemplateDB{}}

	body, _ := json.Marshal(map[string]interface{}{"data": data})
	r := httptest.NewRequest("PUT", "/", bytes.NewBuffer(body))
	r = mux.SetURLVars(r, map[string]string{"submission_id": existing.ID.Hex()})
	rr := httptest.NewRecorder()
	h.UpdateFormSubmissionHandler(rr, r)
	return rr, db
}

func TestFormSubmissionUpdate_AcceptsExactlyTheLimit(t *testing.T) {
	rr, db := putFormSubmission(map[string]interface{}{"narrative": narrativeOf(1000)})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, db.updates)
}

func TestFormSubmissionUpdate_RefusesOneOverTheLimit(t *testing.T) {
	rr, db := putFormSubmission(map[string]interface{}{"narrative": narrativeOf(1001)})

	assertNarrativeRefused(t, rr)
	assert.Zero(t, db.updates, "an over-length edit reached the collection")
}

func TestFormSubmissionUpdate_CountsMultiByteCharactersAsOne(t *testing.T) {
	rr, db := putFormSubmission(map[string]interface{}{"narrative": strings.Repeat(wideRune, 1000)})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, db.updates)
}
