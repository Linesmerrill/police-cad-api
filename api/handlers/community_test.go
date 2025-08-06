package handlers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	mocksdb "github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestCommunity_CommunityHandlerInvalidCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "1234"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

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

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get objectID from Hex", Error: "the provided hex string is not a valid ObjectID"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379b7f4"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Community)
		(*arg).Details.ActivePanics = x

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityHandler)

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

func TestCommunity_CommunityHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379ffff", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379ffff"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get community by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379b7f4"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Community)
		cID, err := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f4")
		if err != nil {
			t.Fatal(err)
		}
		(*arg).ID = cID

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testCommunity := models.Community{}
	json.Unmarshal(rr.Body.Bytes(), &testCommunity)

	assert.Equal(t, "608cafe595eb9dc05379b7f4", testCommunity.ID)
}

func TestCommunity_CommunityByOwnerHandlerInvalidCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/abcd/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "abcd", "owner_id": "1234"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mocked-error"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByCommunityAndOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get objectID from Hex", Error: "the provided hex string is not a valid ObjectID"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityByOwnerHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379b7f4", "owner_id": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Community)
		(*arg).Details.ActivePanics = x

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByCommunityAndOwnerIDHandler)

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

func TestCommunity_CommunityByOwnerHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379ffff/608cafd695eb9dc05379gggg", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379ffff", "owner_id": "608cafd695eb9dc05379gggg"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByCommunityAndOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get community by ID and ownerID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunityByOwnerHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/community/608cafe595eb9dc05379b7f4/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"community_id": "608cafe595eb9dc05379b7f4", "owner_id": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Community)
		cID, err := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f4")
		if err != nil {
			t.Fatal(err)
		}
		(*arg).ID = cID

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunityByCommunityAndOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testCommunity := models.Community{}
	json.Unmarshal(rr.Body.Bytes(), &testCommunity)

	assert.Equal(t, "608cafe595eb9dc05379b7f4", testCommunity.ID)
}

func TestCommunity_CommunitiesByOwnerIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/communities/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"owner_id": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := map[string]interface{}{
		"foo": make(chan int),
	}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Community)
		*arg = []models.Community{{Details: models.CommunityDetails{ActivePanics: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunitiesByOwnerIDHandler)

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

func TestCommunity_CommunitiesByOwnerIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/communities/608cafd695eb9dc05379gggg", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"owner_id": "608cafd695eb9dc05379gggg"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("mongo: no documents in result"))
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunitiesByOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get community by ownerID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestCommunity_CommunitiesByOwnerIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/communities/608cafd695eb9dc05379b7f3", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"owner_id": "608cafd695eb9dc05379b7f3"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Community)
		cID, err := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f4")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Community{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunitiesByOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testCommunity []models.Community
	_ = json.Unmarshal(rr.Body.Bytes(), &testCommunity)

	assert.Equal(t, "608cafe595eb9dc05379b7f4", testCommunity[0].ID)
}

func TestUser_CommunitiesByOwnerIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/communities/608cafd695eb9dc05379bddd", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"owner_id": "608cafd695eb9dc05379bddd"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var cursorHelper databases.CursorHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	cursorHelper = &mocksdb.CursorHelper{}

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	cursorHelper.(*mocksdb.CursorHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Community)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	communityDatabase := databases.NewCommunityDatabase(db)
	u := handlers.Community{
		DB: communityDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.CommunitiesByOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "null"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestCommunity_JoinCommunityHandlerWithNullCommunities(t *testing.T) {
	// Create request body
	requestBody := map[string]string{
		"inviteCode": "TEST123",
		"userId":     "608cafe595eb9dc05379b7f4",
	}
	jsonBody, _ := json.Marshal(requestBody)

	req, err := http.NewRequest("POST", "/api/v1/community/join", bytes.NewBuffer(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	// Mock invite code find
	inviteCodeID, _ := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f4")
	communityID, _ := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f5")
	userID, _ := primitive.ObjectIDFromHex("608cafe595eb9dc05379b7f4")

	inviteCode := models.InviteCode{
		ID:            inviteCodeID,
		Code:          "TEST123",
		CommunityID:   communityID.Hex(),
		RemainingUses: 10,
		ExpiresAt:     nil, // No expiration
	}

	// Mock user with null communities
	user := models.User{
		ID: userID.Hex(),
		Details: models.UserDetails{
			Communities: nil, // This is the key - null communities
		},
	}

	// Mock community
	community := models.Community{
		ID: communityID,
		Details: models.CommunityDetails{
			Name:      "Test Community",
			ImageLink: "test.jpg",
			BanList:   []string{}, // Empty ban list
		},
	}

	// Set up mocks
	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)

	// Mock invite code find and update
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*models.InviteCode)
		*arg = inviteCode
	})
	conn.(*mocksdb.CollectionHelper).On("FindOneAndUpdate", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)

	// Mock user find
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*models.User)
		*arg = user
	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)

	// Mock community find
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*models.Community)
		*arg = community
	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)

	// Mock user update (this should use $set instead of $push for null communities)
	conn.(*mocksdb.CollectionHelper).On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(&mongo.UpdateResult{ModifiedCount: 1}, nil)

	// Mock community update
	conn.(*mocksdb.CollectionHelper).On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(&mongo.UpdateResult{ModifiedCount: 1}, nil)

	db.(*mocksdb.DatabaseHelper).On("Collection", "inviteCodes").Return(conn)
	db.(*mocksdb.DatabaseHelper).On("Collection", "users").Return(conn)
	db.(*mocksdb.DatabaseHelper).On("Collection", "communities").Return(conn)

	// Create handlers
	communityDatabase := databases.NewCommunityDatabase(db)
	userDatabase := databases.NewUserDatabase(db)
	inviteCodeDatabase := databases.NewInviteCodeDatabase(db)

	c := handlers.Community{
		DB:  communityDatabase,
		UDB: userDatabase,
		IDB: inviteCodeDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(c.JoinCommunityHandler)

	handler.ServeHTTP(rr, req)

	// Verify the response
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Verify the response body contains expected data
	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, "joined", response["status"])
	assert.Equal(t, communityID.Hex(), response["communityId"])

	communityData := response["community"].(map[string]interface{})
	assert.Equal(t, communityID.Hex(), communityData["_id"])
	assert.Equal(t, "Test Community", communityData["name"])
	assert.Equal(t, "test.jpg", communityData["imageLink"])
}
