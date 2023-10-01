package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestCivilian_CivilianByIDHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilian/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"civilian_id": "1234"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get objectID from Hex", Error: "the provided hex string is not a valid ObjectID"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CivilianByIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilian/608cafe595eb9dc05379b7f4", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"civilian_id": "608cafe595eb9dc05379b7f4"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Civilian)
		(*arg).Details.CreatedAt = x

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CivilianByIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilian/608cafe595eb9dc05379ffff", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"civilian_id": "608cafe595eb9dc05379ffff"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code:\ngot %v\nwant %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilian by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CivilianByIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilian/5fc51f36c72ff10004dca381", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"civilian_id": "5fc51f36c72ff10004dca381"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Civilian)
		(*arg).ID = "5fc51f36c72ff10004dca381"

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testCivilian := models.Civilian{}
	json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian.ID)
}

func TestCivilian_CivilianHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{Details: models.CivilianDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CivilianHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilians", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CivilianHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CivilianHandlerPaginationSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians?limit=5&page=1", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CivilianHandlerPaginationInvalidPageEntry(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians?limit=5&page='1'", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CivilianHandlerPaginationInvalidPageEntryLessThan1(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians?limit=5&page=-1", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CivilianHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilian", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var cursorHelper databases.CursorHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	cursorHelper = &mocks.CursorHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocks.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CivilianHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByUserIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{Details: models.CivilianDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByUserIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilians with empty active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByUserIDHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/1234?active_community_id=1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilians with active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByUserIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/61be0ebf22cfea7e7550f00e", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByUserIDHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/61be0ebf22cfea7e7550f00e?active_community_id=61c74b7b88e1abdac307bb39", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByUserIDHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/61be0ebf22cfea7e7550f00e?active_community_id=null", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByUserIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var cursorHelper databases.CursorHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	cursorHelper = &mocks.CursorHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocks.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByNameSearchHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{Details: models.CivilianDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByNameSearchHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilian name search with active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByNameSearchHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilian name search with active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByNameSearchHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByNameSearchHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByNameSearchHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=null&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = []models.Civilian{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCivilian []models.Civilian
	_ = json.Unmarshal(rr.Body.Bytes(), &testCivilian)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCivilian[0].ID)
}

func TestCivilian_CiviliansByNameSearchHandlerFailedToFindOneWithEmptyCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=null&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get civilian name search with empty active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCivilian_CiviliansByNameSearchHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/civilians/search?active_community_id=61c74b7b88e1abdac307bb39&first_name=alessandro&last_name=mills&date_of_birth=1987-03-20", nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var cursorHelper databases.CursorHelper

	db = &MockDatabaseHelper{} // can be used as db = &mocks.DatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	cursorHelper = &mocks.CursorHelper{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocks.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Civilian)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "civilians").Return(conn)

	civilianDatabase := databases.NewCivilianDatabase(db)
	u := handlers.Civilian{
		DB: civilianDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CiviliansByNameSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
