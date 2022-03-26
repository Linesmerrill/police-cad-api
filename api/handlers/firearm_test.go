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

func TestFirearm_FirearmByIDHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearm/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"firearm_id": "1234"})
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
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmByIDHandler)

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

func TestFirearm_FirearmByIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearm/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"firearm_id": "5fc51f58c72ff10004dca382"})
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
		arg := args.Get(0).(**models.Firearm)
		(*arg).Details.CreatedAt = x

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmByIDHandler)

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

func TestFirearm_FirearmByIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearm/5fc51f58c72ff10004dca999", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"firearm_id": "5fc51f58c72ff10004dca999"})
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
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code:\ngot %v\nwant %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get firearm by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestFirearm_FirearmByIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearm/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"firearm_id": "5fc51f58c72ff10004dca382"})
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
		arg := args.Get(0).(**models.Firearm)
		(*arg).ID = "5fc51f58c72ff10004dca382"

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testFirearm := models.Firearm{}
	_ = json.Unmarshal(rr.Body.Bytes(), &testFirearm)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testFirearm.ID)
}

func TestFirearm_FirearmHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{Details: models.FirearmDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmHandler)

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

func TestFirearm_FirearmHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms", nil)
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
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get firearms", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestFirearm_FirearmHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{ID: "5fc51f58c72ff10004dca382"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testFirearm []models.Firearm
	_ = json.Unmarshal(rr.Body.Bytes(), &testFirearm)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testFirearm[0].ID)
}

func TestFirearm_FirearmHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearm", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestFirearm_FirearmsByUserIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/1234", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{Details: models.FirearmDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

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

func TestFirearm_FirearmsByUserIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/1234", nil)
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
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get firearms with empty active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestFirearm_FirearmsByUserIDHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/1234?active_community_id=1234", nil)
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
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get firearms with active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestFirearm_FirearmsByUserIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/61be0ebf22cfea7e7550f00e", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testFirearm []models.Firearm
	_ = json.Unmarshal(rr.Body.Bytes(), &testFirearm)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testFirearm[0].ID)
}

func TestFirearm_FirearmsByUserIDHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/61be0ebf22cfea7e7550f00e?active_community_id=61c74b7b88e1abdac307bb39", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testFirearm []models.Firearm
	_ = json.Unmarshal(rr.Body.Bytes(), &testFirearm)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testFirearm[0].ID)
}

func TestFirearm_FirearmsByUserIDHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/61be0ebf22cfea7e7550f00e?active_community_id=null", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = []models.Firearm{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testFirearm []models.Firearm
	_ = json.Unmarshal(rr.Body.Bytes(), &testFirearm)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testFirearm[0].ID)
}

func TestFirearm_FirearmsByUserIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/firearms/user/1234", nil)
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
		arg := args.Get(0).(*[]models.Firearm)
		*arg = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "firearms").Return(conn)

	firearmDatabase := databases.NewFirearmDatabase(db)
	u := handlers.Firearm{
		DB: firearmDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.FirearmsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
