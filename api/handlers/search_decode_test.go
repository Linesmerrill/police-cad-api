package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// goodUser is a user document that decodes cleanly into models.User.
func goodUser(name string) bson.M {
	return bson.M{
		"_id": primitive.NewObjectID(),
		"user": bson.M{
			"name":     name,
			"username": name,
		},
	}
}

// poisonUser stores a string where the model declares a bool. That is the
// mismatch class that actually fails a decode, and it fails the WHOLE document,
// not just the offending field.
//
// Worth recording what does NOT break it, because it is the obvious guess: an
// ObjectID sitting in a string field decodes fine. The driver yields an empty
// string rather than an error, so that particular shape is a silent data bug,
// never a 500.
func poisonUser(name string) bson.M {
	return bson.M{
		"_id": primitive.NewObjectID(),
		"user": bson.M{
			"name":          name,
			"username":      name,
			"isDeactivated": "not-a-bool",
		},
	}
}

func cursorOf(t *testing.T, docs ...bson.M) databases.MongoCursor {
	t.Helper()
	raw := make([]interface{}, 0, len(docs))
	for _, d := range docs {
		raw = append(raw, d)
	}
	cur, err := databases.NewMongoCursorFromDocuments(raw)
	assert.NoError(t, err)
	return cur
}

// Establishes the premise: cursor.All really is all-or-nothing, so a single bad
// record returned nothing at all and 500'd the endpoint for everybody.
func TestCursorAllFailsOnOneBadDocument(t *testing.T) {
	cur := cursorOf(t, goodUser("alice"), poisonUser("bob"), goodUser("carol"))

	var users []models.User
	err := cur.All(context.Background(), &users)

	assert.Error(t, err, "one malformed document fails the entire batch")
}

func TestDecodeTolerantlySkipsTheBadDocument(t *testing.T) {
	cur := cursorOf(t, goodUser("alice"), poisonUser("bob"), goodUser("carol"))

	users := decodeTolerantly[models.User](context.Background(), cur, "test")

	assert.Len(t, users, 2, "the two good users still come back")
	assert.Equal(t, "alice", users[0].Details.Name)
	assert.Equal(t, "carol", users[1].Details.Name)
}

func TestDecodeTolerantlyAllGood(t *testing.T) {
	cur := cursorOf(t, goodUser("alice"), goodUser("bob"))

	users := decodeTolerantly[models.User](context.Background(), cur, "test")

	assert.Len(t, users, 2)
}

func TestDecodeTolerantlyAllBadReturnsEmptyNotNil(t *testing.T) {
	cur := cursorOf(t, poisonUser("bob"), poisonUser("dave"))

	users := decodeTolerantly[models.User](context.Background(), cur, "test")

	// Empty, never nil — callers serialize this straight to JSON and a nil
	// slice marshals as null instead of [].
	assert.NotNil(t, users)
	assert.Len(t, users, 0)
}

func TestDecodeTolerantlyEmptyCursor(t *testing.T) {
	cur := cursorOf(t)

	users := decodeTolerantly[models.User](context.Background(), cur, "test")

	assert.NotNil(t, users)
	assert.Len(t, users, 0)
}

func TestDecodeTolerantlyWorksForCommunities(t *testing.T) {
	good := bson.M{"_id": primitive.NewObjectID(), "community": bson.M{"name": "RP Kings"}}
	// activeSignal100 is a bool on the model; a string there fails the decode.
	bad := bson.M{"_id": primitive.NewObjectID(), "community": bson.M{
		"name": "Broken", "activeSignal100": "definitely-not-a-bool",
	}}

	cur := cursorOf(t, good, bad)
	communities := decodeTolerantly[models.Community](context.Background(), cur, "test")

	assert.Len(t, communities, 1)
	assert.Equal(t, "RP Kings", communities[0].Details.Name)
}

func TestToIDString(t *testing.T) {
	oid := primitive.NewObjectID()
	assert.Equal(t, oid.Hex(), toIDString(oid))
	assert.Equal(t, "abc", toIDString("abc"))
	assert.Equal(t, "unprintable", toIDString(42))
}
