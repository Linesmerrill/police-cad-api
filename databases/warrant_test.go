package databases_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestNewWarrantDatabase(t *testing.T) {
	_ = os.Setenv("DB_URI", "mongodb://127.0.0.1:27017")
	_ = os.Setenv("DB_NAME", "test")
	conf := config.New()

	dbClient, err := databases.NewClient(conf)
	assert.NoError(t, err)

	db := databases.NewDatabase(conf, dbClient)

	userDB := databases.NewWarrantDatabase(db)

	assert.NotEmpty(t, userDB)
}

func TestWarrantDatabase_FindOne(t *testing.T) {

	// define variables for interfaces
	var dbHelper databases.DatabaseHelper
	var collectionHelper databases.CollectionHelper
	var srHelperErr databases.SingleResultHelper
	var srHelperCorrect databases.SingleResultHelper

	// set interfaces implementation to mocked structures
	dbHelper = &mocks.DatabaseHelper{}
	collectionHelper = &mocks.CollectionHelper{}
	srHelperErr = &mocks.SingleResultHelper{}
	srHelperCorrect = &mocks.SingleResultHelper{}

	srHelperErr.(*mocks.SingleResultHelper).
		On("Decode", mock.Anything).
		Return(errors.New("mocked-error"))

	srHelperCorrect.(*mocks.SingleResultHelper).
		On("Decode", mock.Anything).
		Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(**models.Warrant)
		(*arg).ID = "mocked-user"
	})

	collectionHelper.(*mocks.CollectionHelper).
		On("FindOne", context.Background(), bson.M{"error": true}).
		Return(srHelperErr)

	collectionHelper.(*mocks.CollectionHelper).
		On("FindOne", context.Background(), bson.M{"error": false}).
		Return(srHelperCorrect)

	dbHelper.(*mocks.DatabaseHelper).
		On("Collection", "warrants").Return(collectionHelper)

	// Create new database with mocked Database interface
	userDba := databases.NewWarrantDatabase(dbHelper)

	// Call method with defined filter, that in our mocked function returns
	// mocked-error
	user, err := userDba.FindOne(context.Background(), bson.M{"error": true})

	assert.Empty(t, user)
	assert.EqualError(t, err, "mocked-error")

	// Now call the same function with different filter for correct
	// result
	user, err = userDba.FindOne(context.Background(), bson.M{"error": false})

	assert.Equal(t, &models.Warrant{ID: "mocked-user"}, user)
	assert.NoError(t, err)
}

func TestWarrantDatabase_Find(t *testing.T) {

	// define variables for interfaces
	var dbHelper databases.DatabaseHelper
	var collectionHelper databases.CollectionHelper
	var srHelperErr databases.SingleResultHelper
	var srHelperCorrect databases.SingleResultHelper

	// set interfaces implementation to mocked structures
	dbHelper = &mocks.DatabaseHelper{}
	collectionHelper = &mocks.CollectionHelper{}
	srHelperErr = &mocks.SingleResultHelper{}
	srHelperCorrect = &mocks.SingleResultHelper{}

	srHelperErr.(*mocks.SingleResultHelper).
		On("Decode", mock.Anything).
		Return(errors.New("mocked-error"))

	srHelperCorrect.(*mocks.SingleResultHelper).
		On("Decode", mock.Anything).
		Return(nil).Run(func(args mock.Arguments) {
		arg := args.Get(0).(*[]models.Warrant)
		*arg = []models.Warrant{{ID: "mocked-user"}}
	})

	collectionHelper.(*mocks.CollectionHelper).
		On("Find", context.Background(), bson.M{"error": true}).
		Return(srHelperErr)

	collectionHelper.(*mocks.CollectionHelper).
		On("Find", context.Background(), bson.M{"error": false}).
		Return(srHelperCorrect)

	dbHelper.(*mocks.DatabaseHelper).
		On("Collection", "warrants").Return(collectionHelper)

	// Create new database with mocked Database interface
	userDba := databases.NewWarrantDatabase(dbHelper)

	// Call method with defined filter, that in our mocked function returns
	// mocked-error
	user, err := userDba.Find(context.Background(), bson.M{"error": true})

	assert.Empty(t, user)
	assert.EqualError(t, err, "mocked-error")

	// Now call the same function with different filter for correct
	// result
	user, err = userDba.Find(context.Background(), bson.M{"error": false})

	assert.Equal(t, []models.Warrant{{ID: "mocked-user"}}, user)
	assert.NoError(t, err)
}
