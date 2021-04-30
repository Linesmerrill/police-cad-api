package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/models"
	"github.com/linesmerrill/police-cad-api/mongodb"
	"github.com/linesmerrill/police-cad-api/mongodb/mocks"
)

func TestCommunityDatabase_FindOne(t *testing.T) {

	// define variables for interfaces
	var dbHelper collections.DatabaseHelper
	var collectionHelper collections.CollectionHelper
	var srHelperErr collections.SingleResultHelper
	var srHelperCorrect collections.SingleResultHelper

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
		arg := args.Get(0).(**models.Community)
		(*arg).ID = "mocked-user"
	})

	collectionHelper.(*mocks.CollectionHelper).
		On("FindOne", context.Background(), bson.M{"error": true}).
		Return(srHelperErr)

	collectionHelper.(*mocks.CollectionHelper).
		On("FindOne", context.Background(), bson.M{"error": false}).
		Return(srHelperCorrect)

	dbHelper.(*mocks.DatabaseHelper).
		On("Collection", "communities").Return(collectionHelper)

	// Create new database with mocked Database interface
	userDba := collections.NewCommunityDatabase(dbHelper)

	// Call method with defined filter, that in our mocked function returns
	// mocked-error
	user, err := userDba.FindOne(context.Background(), bson.M{"error": true})

	assert.Empty(t, user)
	assert.EqualError(t, err, "mocked-error")

	// Now call the same function with different different filter for correct
	// result
	user, err = userDba.FindOne(context.Background(), bson.M{"error": false})

	assert.Equal(t, &models.Community{ID: "mocked-user"}, user)
	assert.NoError(t, err)
}
