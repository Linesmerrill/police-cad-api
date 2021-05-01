package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linesmerrill/police-cad-api/models"

	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"

	"github.com/linesmerrill/police-cad-api/api/handlers"
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

func TestUser_UserHandler(t *testing.T) {
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
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to get user by ID, mocked-error"}null`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestUser_UserHandlerMarshalError(t *testing.T) {
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

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.User)
		(*arg).UserInner.CreatedAt = x
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

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to marshal response, json: unsupported type: chan int"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}
