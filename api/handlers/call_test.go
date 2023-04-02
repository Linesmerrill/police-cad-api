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

func TestCall_CallByIDHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/call/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"call_id": "1234"})
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
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallByIDHandler)

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

func TestCall_CallByIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/call/608cafe595eb9dc05379b7f4", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"call_id": "608cafe595eb9dc05379b7f4"})
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
		arg := args.Get(0).(**models.Call)
		(*arg).Details.CreatedAt = x

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallByIDHandler)

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

func TestCall_CallByIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/call/608cafe595eb9dc05379ffff", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"call_id": "608cafe595eb9dc05379ffff"})
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
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code:\ngot %v\nwant %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get call by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCall_CallByIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/call/5fc51f36c72ff10004dca381", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"call_id": "5fc51f36c72ff10004dca381"})
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
		arg := args.Get(0).(**models.Call)
		(*arg).ID = "5fc51f36c72ff10004dca381"

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testCall := models.Call{}
	json.Unmarshal(rr.Body.Bytes(), &testCall)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCall.ID)
}

func TestCall_CallHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls", nil)
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
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{Details: models.CallDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallHandler)

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

func TestCall_CallHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls", nil)
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
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get calls", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCall_CallHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls", nil)
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
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCall []models.Call
	_ = json.Unmarshal(rr.Body.Bytes(), &testCall)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCall[0].ID)
}

func TestCall_CallHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/call", nil)
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
		arg := args.Get(0).(*[]models.Call)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCall_CallsByCommunityIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "61c74b7b88e1abdac307bb39",
	})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{}
	client = &mocks.ClientHelper{}
	conn = &mocks.CollectionHelper{}
	singleResultHelper = &mocks.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{Details: models.CallDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

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

func TestCall_CallsByCommunityIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "1234",
	})
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
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get calls with community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCall_CallsByCommunityIDHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}?status=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "1234",
	})
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
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get calls with community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCall_CallsByCommunityIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "61c74b7b88e1abdac307bb39",
	})

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
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCall []models.Call
	_ = json.Unmarshal(rr.Body.Bytes(), &testCall)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCall[0].ID)
}

func TestCall_CallsByCommunityIDHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}?status=false", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "61c74b7b88e1abdac307bb39",
	})

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
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCall []models.Call
	_ = json.Unmarshal(rr.Body.Bytes(), &testCall)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCall[0].ID)
}

func TestCall_CallsByCommunityIDHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}?status=null", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "61c74b7b88e1abdac307bb39",
	})

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
		arg := args.Get(0).(*[]models.Call)
		*arg = []models.Call{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCall []models.Call
	_ = json.Unmarshal(rr.Body.Bytes(), &testCall)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testCall[0].ID)
}

func TestCall_CallsByCommunityIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/calls/community/{community_id}", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = mux.SetURLVars(req, map[string]string{
		"community_id": "1234",
	})

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
		arg := args.Get(0).(*[]models.Call)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "calls").Return(conn)

	callDatabase := databases.NewCallDatabase(db)
	u := handlers.Call{
		DB: callDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CallsByCommunityIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
