package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases"
	mocksdb "github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestVehicle_VehicleByIDHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicle/1234", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"vehicle_id": "1234"})
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleByIDHandler)

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

func TestVehicle_VehicleByIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicle/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"vehicle_id": "5fc51f58c72ff10004dca382"})
	req.Header.Set("Authorization", "Bearer abc123")

	var db databases.DatabaseHelper
	var client databases.ClientHelper
	var conn databases.CollectionHelper
	var singleResultHelper databases.SingleResultHelper

	db = &mocksdb.DatabaseHelper{} // can be used as db = &mocksdb.DatabaseHelper{}
	client = &mocksdb.ClientHelper{}
	conn = &mocksdb.CollectionHelper{}
	singleResultHelper = &mocksdb.SingleResultHelper{}

	x := primitive.NewDateTimeFromTime(time.Now())

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Vehicle)
		(*arg).Details.CreatedAt = x

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleByIDHandler)

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

func TestVehicle_VehicleByIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicle/5fc51f58c72ff10004dca999", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"vehicle_id": "5fc51f58c72ff10004dca999"})
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code:\ngot %v\nwant %v", status, http.StatusBadRequest)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicle by ID", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehicleByIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicle/5fc51f58c72ff10004dca382", nil)
	if err != nil {
		t.Fatal(err)
	}

	req = mux.SetURLVars(req, map[string]string{"vehicle_id": "5fc51f58c72ff10004dca382"})
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
		arg := args.Get(0).(**models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f58c72ff10004dca382")
		if err != nil {
			t.Fatal(err)
		}
		(*arg).ID = cID

	})
	conn.(*mocksdb.CollectionHelper).On("FindOne", mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleByIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	testVehicle := models.Vehicle{}
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testVehicle.ID)
}

func TestVehicle_VehicleHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles", nil)
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

	x := primitive.NewDateTimeFromTime(time.Now())

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = []models.Vehicle{{Details: models.VehicleDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleHandler)

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

func TestVehicle_VehicleHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles", nil)
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicles", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehicleHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f58c72ff10004dca382")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f58c72ff10004dca382", testVehicle[0].ID)
}

func TestVehicle_VehicleHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicle", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByUserIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/1234", nil)
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

	x := primitive.NewDateTimeFromTime(time.Now())

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = []models.Vehicle{{Details: models.VehicleDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

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

func TestVehicle_VehiclesByUserIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/1234", nil)
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicles with empty active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByUserIDHandlerActiveCommunityIDFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/1234?active_community_id=1234", nil)
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicles with active community id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByUserIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/61be0ebf22cfea7e7550f00e", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f36c72ff10004dca381")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testVehicle[0].ID)
}

func TestVehicle_VehiclesByUserIDHandlerSuccessWithActiveCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/61be0ebf22cfea7e7550f00e?active_community_id=61c74b7b88e1abdac307bb39", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f36c72ff10004dca381")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testVehicle[0].ID)
}

func TestVehicle_VehiclesByUserIDHandlerSuccessWithNullCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/61be0ebf22cfea7e7550f00e?active_community_id=null", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f36c72ff10004dca381")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testVehicle[0].ID)
}

func TestVehicle_VehiclesByUserIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/user/1234", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByUserIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByRegisteredOwnerIDHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/registered-owner/1234", nil)
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

	x := primitive.NewDateTimeFromTime(time.Now())

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = []models.Vehicle{{Details: models.VehicleDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByRegisteredOwnerIDHandler)

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

func TestVehicle_VehiclesByRegisteredOwnerIDHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/registered-owner/1234", nil)
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
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByRegisteredOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicles by registered owner id", Error: "mongo: no documents in result"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByRegisteredOwnerIDHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/registered-owner/61be0ebf22cfea7e7550f00e", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f36c72ff10004dca381")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByRegisteredOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testVehicle[0].ID)
}

func TestVehicle_VehiclesByRegisteredOwnerIDHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/registered-owner/1234", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehiclesByRegisteredOwnerIDHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByPlateSearchHandlerJsonMarshalError(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/search", nil)
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

	x := primitive.NewDateTimeFromTime(time.Now())

	client.(*mocksdb.ClientHelper).On("StartSession").Return(nil, errors.New("mocked-error"))
	db.(*mocksdb.DatabaseHelper).On("Client").Return(client)
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = []models.Vehicle{{Details: models.VehicleDetails{CreatedAt: x}}}
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleSearchHandler)

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

func TestVehicle_VehiclesByPlateSearchHandlerFailedToFindOne(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/search?active_community_id=61c74b7b88e1abdac307bb39", nil)
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
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("failed to get vehicle plate search with empty active community id"))
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicle plate search with active community id", Error: "failed to get vehicle plate search with empty active community id"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByPlateSearchHandlerFailedToFindOneWithEmptyCommunityID(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/search", nil)
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
	singleResultHelper.(*mocksdb.SingleResultHelper).On("Decode", mock.Anything).Return(errors.New("failed to get vehicle plate search with empty active community id"))
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusNotFound {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusNotFound)
	}

	expected := models.ErrorMessageResponse{Response: models.MessageError{Message: "failed to get vehicle plate search with empty active community id", Error: "failed to get vehicle plate search with empty active community id"}}
	b, _ := json.Marshal(expected)
	if rr.Body.String() != string(b) {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body.String(), expected)
	}
}

func TestVehicle_VehiclesByPlateSearchHandlerSuccess(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/search?active_community_id=61be0ebf22cfea7e7550f00e", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		cID, err := primitive.ObjectIDFromHex("5fc51f36c72ff10004dca381")
		if err != nil {
			t.Fatal(err)
		}
		*arg = []models.Vehicle{{ID: cID}}

	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(singleResultHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusBadRequest)
	}

	var testVehicle []models.Vehicle
	_ = json.Unmarshal(rr.Body.Bytes(), &testVehicle)

	assert.Equal(t, "5fc51f36c72ff10004dca381", testVehicle[0].ID)
}

func TestVehicle_VehiclesByPlateSearchHandlerEmptyResponse(t *testing.T) {
	req, err := http.NewRequest("GET", "/api/v1/vehicles/search", nil)
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
		arg := args.Get(0).(*[]models.Vehicle)
		*arg = nil
	})
	conn.(*mocksdb.CollectionHelper).On("Find", mock.Anything, mock.Anything, mock.Anything).Return(cursorHelper)
	db.(*mocksdb.DatabaseHelper).On("Collection", "vehicles").Return(conn)

	vehicleDatabase := databases.NewVehicleDatabase(db)
	u := handlers.Vehicle{
		DB: vehicleDatabase,
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(u.VehicleSearchHandler)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	expected := "[]"
	if rr.Body.String() != expected {
		t.Errorf("handler returned unexpected body: \ngot: %v \nwant: %v", rr.Body.String(), expected)
	}
}
