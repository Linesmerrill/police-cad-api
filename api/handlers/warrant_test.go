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
	mocksdb "github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestWarrant_WarrantByIDHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrant/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"warrant_id": "1234"})
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantByIDHandler)

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

func TestWarrant_WarrantByIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrant/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"warrant_id": "5fc51f58c72ff10004dca382"})
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
		arg := args.Get(0).(**models.Warrant)
		(*arg).Details.CreatedAt = x

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantByIDHandler)

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

func TestWarrant_WarrantByIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrant/5fc51f58c72ff10004dca999", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"warrant_id": "5fc51f58c72ff10004dca999"})
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code:\ngot %v\nwant %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get warrant by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestWarrant_WarrantByIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrant/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"warrant_id": "5fc51f58c72ff10004dca382"})
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
		arg := args.Get(0).(**models.Warrant)
		(*arg).ID = "5fc51f58c72ff10004dca382"

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testWarrant := models.Warrant{}
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testWarrant.ID)
}

func TestWarrant_WarrantHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants", nil)
	if err != nil {
		t.Fatal(err)
	}
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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{Details: models.WarrantDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantHandler)

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

func TestWarrant_WarrantHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants", nil)
	if err != nil {
		t.Fatal(err)
	}

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
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get warrants", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestWarrant_WarrantHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "5fc51f58c72ff10004dca382"}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testWarrant []models.Warrant
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testWarrant[0].ID)
}

func TestWarrant_WarrantHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrant", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestWarrant_WarrantsByUserIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}
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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{Details: models.WarrantDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

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

func TestWarrant_WarrantsByUserIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

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
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get warrants", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestWarrant_WarrantsByUserIDHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/1234?active_community_id=1234", nil)
	if err != nil {
		t.Fatal(err)
	}

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
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get warrants", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestWarrant_WarrantsByUserIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/61be0ebf22cfea7e7550f00e", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testWarrant []models.Warrant
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testWarrant[0].ID)
}

func TestWarrant_WarrantsByUserIDHandlerSuccessWithStatusFalse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/61be0ebf22cfea7e7550f00e?active_community_id=61c74b7b88e1abdac307bb39&status=false", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testWarrant []models.Warrant
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testWarrant[0].ID)
}

func TestWarrant_WarrantsByUserIDHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/61be0ebf22cfea7e7550f00e?active_community_id=61c74b7b88e1abdac307bb39", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testWarrant []models.Warrant
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testWarrant[0].ID)
}

func TestWarrant_WarrantsByUserIDHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/61be0ebf22cfea7e7550f00e?active_community_id=null", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "5fc51f36c72ff10004dca381"}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testWarrant []models.Warrant
	_ = json.Unmarshal(rr.Body.Bytes(), &testWarrant)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testWarrant[0].ID)
}

func TestWarrant_WarrantsByUserIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/warrants/user/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

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
		arg := args.Get(0).(*[]models.Warrant)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "warrants").Return(conn)

	warrantDatabase := databases.NewWarrantDatabase(db)
	u := handlers.Warrant{
		DB: warrantDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.WarrantsByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
