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

type MockDatabaseHelper struct {
	mock.Mock
}

// Client provides a mock function.
func (_m *MockDatabaseHelper) Client() databases.ClientHelper {
	ret := _m.Called()

	var r0 databases.ClientHelper
	if rf, ok := ret.Get(0).(func() databases.ClientHelper); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(databases.ClientHelper)
		}
	}

	return r0
}

// Collection provides a mock function.
func (_m *MockDatabaseHelper) Collection(name string) databases.CollectionHelper {
	ret := _m.Called(name)

	var r0 databases.CollectionHelper
	if rf, ok := ret.Get(0).(func(string) databases.CollectionHelper); ok {
		r0 = rf(name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(databases.CollectionHelper)
		}
	}

	return r0
}

func TestUser_UserHandlerInvalidID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/asdf", nil)
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
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UserHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UserHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
		arg := args.Get(0).(**models.User)
		(*arg).Details.CreatedAt = x
	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UserHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
	}

	expected := `{"response": "failed to marshal response, json: unsupported type: chan int"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UserHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UserHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := `{"response": "failed to get user by ID, mongo: no documents in result"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UserHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
		arg := args.Get(0).(**models.User)
		(*arg).ID = "608cafd695eb9dc05379b7f3"
	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UserHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	testUser := models.User{}
	json.Unmarshal(rr.Body.Bytes(), &testUser)

	assert.Equal(t, "608cafd695eb9dc05379b7f3", testUser.ID)
}

func TestUser_UsersFindAllHandlerInvalidID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/users/asdf", nil)
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
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UsersFindAllHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := `{"response": "failed to get user by ID, mocked-error"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UsersFindAllHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
		arg := args.Get(0).(*[]models.User)
		(*arg) = []models.User{{Details: models.UserDetails{CreatedAt: x}}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UsersFindAllHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
	}

	expected := `{"response": "failed to marshal response, json: unsupported type: chan int"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UsersFindAllHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UsersFindAllHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := `{"response": "failed to get user by ID, mongo: no documents in result"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestUser_UsersFindAllHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379b7f3"})
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
		arg := args.Get(0).(*[]models.User)
		(*arg) = []models.User{{ID: "608cafd695eb9dc05379b7f3"}}
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UsersFindAllHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var testUser []models.User
	json.Unmarshal(rr.Body.Bytes(), &testUser)

	assert.Equal(t, "608cafd695eb9dc05379b7f3", testUser[0].ID)
}

func TestUser_UsersFindAllHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/user/608cafd695eb9dc05379bddd", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"user_id": "608cafd695eb9dc05379bddd"})
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
		arg := args.Get(0).(*[]models.User)
		(*arg) = nil
	})
	conn.(*mocks.CollectionHelper).On("Find", mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*MockDatabaseHelper).On("Collection", "users").Return(conn)

	userDatabase := databases.NewUserDatabase(db)
	u := handlers.User{
		DB: userDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.UsersFindAllHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
