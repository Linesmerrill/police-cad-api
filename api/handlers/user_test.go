package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	mocksdb "github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

type MockDatabaseHelper struct {
	mock.Mock
}

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
func (_m *MockDatabaseHelper) Collection(name string) *databases.MongoCollection {
	ret := _m.Called(name)

	var r0 *databases.MongoCollection
	if rf, ok := ret.Get(0).(func(string) *databases.MongoCollection); ok {
		r0 = rf(name)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*databases.MongoCollection)
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get objectID from Hex", Error: "the provided hex string is not a valid ObjectID"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.User)
		(*arg).Details.CreatedAt = x
	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get user by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.User)
		(*arg).ID = "608cafd695eb9dc05379b7f3"
	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get user by ID", Error: "mocked-error"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.User)
		(*arg) = []models.User{{Details: models.UserDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to marshal response", Error: "json: unsupported type: chan int"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get user by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	cursorHelper = &mocksdb.CursorHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocksdb.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.User)
		(*arg) = []models.User{{ID: "608cafd695eb9dc05379b7f3"}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
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

	db = &MockDatabaseHelper{} // can be used as db = &MockDatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	cursorHelper = &mocksdb.CursorHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocksdb.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.User)
		(*arg) = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
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

func TestUser_AddCommunityToUserHandler_Success(t *testing.T) {
	// Setup request
	reqBody := `{"communityId": "608cafd695eb9dc05379b7f4", "status": "approved"}`
	req, err := http.NewRequest("PUT", "/api/v1/user/608cafd695eb9dc05379b7f3/communities", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"userId": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Content-Type", "application/json")

	// Setup mocks
	db := &mocksdb.DatabaseHelper{}
	userConn := &mocksdb.CollectionHelper{}
	communityConn := &mocksdb.CollectionHelper{}
	singleResultHelper := &mocksdb.SingleResultHelper{}

	// Mock user and community collections
	db.On("Collection", "users").Return(userConn)
	db.On("Collection", "communities").Return(communityConn)

	// Mock community exists
	communityConn.On("FindOne", mock.Anything, bson.M{"_id": mock.Anything}).Return(singleResultHelper)
	singleResultHelper.On("Decode", mock.Anything).Return(nil)

	// Mock user fetch
	user := models.User{
		Details: models.UserDetails{
			Communities: []models.UserCommunity{}, // Empty communities array
		},
	}
	singleResultHelper.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		// Populate the user struct
		userPtr := args.Get(0).(*models.User)
		*userPtr = user
	}).Return(nil)

	// Mock user update ($set or $push)
	userConn.On("UpdateOne", mock.Anything, bson.M{"_id": mock.Anything}, mock.Anything, mock.Anything).Return(&mongo.UpdateResult{ModifiedCount: 1}, nil)

	// Mock community membersCount increment
	communityConn.On("UpdateOne", mock.Anything, bson.M{"_id": mock.Anything}, bson.M{"$inc": bson.M{"community.membersCount": 1}}, mock.Anything).Return(&mongo.UpdateResult{ModifiedCount: 1}, nil)

	// Initialize handler
	userDatabase := databases.NewUserDatabase(db)
	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.User{
		DB:  userDatabase,
		CDB: communityDatabase,
	}

	// Execute handler
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.AddCommunityToUserHandler)
	handler.ServeHTTP(rr, req)

	// Assertions
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"message": "Community added successfully"}`, rr.Body.String())

	// Verify mocks
	db.AssertExpectations(t)
	userConn.AssertExpectations(t)
	communityConn.AssertExpectations(t)
	singleResultHelper.AssertExpectations(t)
}

func TestUser_AddCommunityToUserHandler_InvalidUserID(t *testing.T) {
	reqBody := `{"communityId": "608cafd695eb9dc05379b7f4", "status": "approved"}`
	req, err := http.NewRequest("PUT", "/api/v1/user/invalidUserId/communities", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"userId": "invalidUserId"})
	req.Header.Set("Content-Type", "application/json")

	var db databases.DatabaseHelper
	db = &MockDatabaseHelper{}

	userDatabase := databases.NewUserDatabase(db)
	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.User{
		DB:  userDatabase,
		CDB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.AddCommunityToUserHandler)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "failed to get objectID from Hex")
}

func TestUser_AddCommunityToUserHandler_InvalidCommunityID(t *testing.T) {
	reqBody := `{"communityId": "invalidCommunityId", "status": "approved"}`
	req, err := http.NewRequest("PUT", "/api/v1/user/608cafd695eb9dc05379b7f3/communities", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"userId": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Content-Type", "application/json")

	var db databases.DatabaseHelper
	db = &MockDatabaseHelper{}

	userDatabase := databases.NewUserDatabase(db)
	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.User{
		DB:  userDatabase,
		CDB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.AddCommunityToUserHandler)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "failed to get community objectID from Hex")
}

func TestUser_AddCommunityToUserHandler_CommunityAlreadyExists(t *testing.T) {
	reqBody := `{"communityId": "608cafd695eb9dc05379b7f4", "status": "approved"}`
	req, err := http.NewRequest("PUT", "/api/v1/user/608cafd695eb9dc05379b7f3/communities", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"userId": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Content-Type", "application/json")

	var db databases.DatabaseHelper
	var userConn, communityConn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &MockDatabaseHelper{}
	userConn = &mocksdb.CollectionHelper{}
	communityConn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	// Mock user and community database interactions
	db.(*MockDatabaseHelper).On("Collection", "users").Return(userConn)
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(communityConn)

	// Mock community exists
	communityConn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil)

	// Mock user already has the community
	userConn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*models.User)
		arg.Details.Communities = []models.UserCommunity{
			{CommunityID: "608cafd695eb9dc05379b7f4", Status: "approved"},
		}
	})

	userDatabase := databases.NewUserDatabase(db)
	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.User{
		DB:  userDatabase,
		CDB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.AddCommunityToUserHandler)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "community status not updated")
}
