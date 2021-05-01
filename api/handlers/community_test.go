package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/models"

	"github.com/stretchr/testify/mock"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"

	"github.com/linesmerrill/police-cad-api/api/handlers"
)

func TestCommunity_CommunityHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/1234", nil)
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
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to get community by ID, mocked-error"}null`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityHandlerMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/1234", nil)
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
		arg := args.Get(0).(**models.Community)
		(*arg).CommunityInner.ActivePanics = x

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to marshal response, json: unsupported type: chan int"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityByOwnerHandlerBadRequestError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4/608cafd695eb9dc05379b7f3", nil)
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
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByOwnerHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to get community by ID and ownerID, mocked-error"}null`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityByOwnerHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4/608cafd695eb9dc05379b7f3", nil)
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
		arg := args.Get(0).(**models.Community)
		(*arg).CommunityInner.ActivePanics = x

	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByOwnerHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"response": "failed to marshal response, json: unsupported type: chan int"}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityByOwnerHandlerMultiError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4/608cafd695eb9dc05379b7f3", nil)
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

	//resp := models.Community{}

	client.(*mocks.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*MockDatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocks.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Community)
		(*arg).CommunityInner.CreatedAt = primitive.NewDateTimeFromTime(time.Date(2020, 1, 1, 12, 00, 00, 00, time.UTC))
		arg = args.Get(0).(**models.Community)
		(*arg).CommunityInner.UpdatedAt = primitive.NewDateTimeFromTime(time.Date(2020, 1, 1, 12, 00, 00, 00, time.UTC))
	})
	conn.(*mocks.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*MockDatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByOwnerHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := `{"response": "failed to get objectID from Hex, the provided hex string is not a valid ObjectID"}{"_id":"","community":{"name":"","ownerID":"","code":"","activePanics":null,"activeSignal100":false,"createdAt":"2020-01-01T05:00:00-07:00","updatedAt":"2020-01-01T05:00:00-07:00"},"__v":0}`
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: got %v\nwant %v", rr.Body.String(), expected)
	}
}
