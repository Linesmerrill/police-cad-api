package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"

	gerrors "github.com/shaj13/go-guardian/errors"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

func TestLoginCache_AnswersOnlyWhileTheStoredHashIsUnchanged(t *testing.T) {
	c := newLoginCache(time.Hour, 10)
	c.remember("u1", "hash-1", "hunter2")

	assert.True(t, c.matches("u1", "hash-1", "hunter2"))
	assert.False(t, c.matches("u1", "hash-1", "wrong"), "a different password")
	assert.False(t, c.matches("u1", "hash-2", "hunter2"), "the password was changed since")
	assert.False(t, c.matches("u2", "hash-1", "hunter2"), "a different user")
}

// The old cache held the password itself. This one must not.
func TestLoginCache_NeverHoldsThePassword(t *testing.T) {
	c := newLoginCache(time.Hour, 10)
	c.remember("u1", "hash-1", "hunter2")
	assert.NotContains(t, fmt.Sprintf("%v", c.entries), "hunter2")
}

func TestLoginCache_Expires(t *testing.T) {
	c := newLoginCache(time.Minute, 10)
	now := time.Now()
	c.now = func() time.Time { return now }
	c.remember("u1", "hash-1", "hunter2")

	c.now = func() time.Time { return now.Add(2 * time.Minute) }
	assert.False(t, c.matches("u1", "hash-1", "hunter2"))
}

func TestLoginCache_StaysBounded(t *testing.T) {
	c := newLoginCache(time.Hour, 3)
	for i := 0; i < 10; i++ {
		c.remember(fmt.Sprintf("u%d", i), "h", "p")
	}
	assert.LessOrEqual(t, len(c.entries), 3)
	assert.True(t, c.matches("u9", "h", "p"), "the newest login is kept")
}

// Without a cache every login simply pays for bcrypt.
func TestLoginCache_NilIsANoOp(t *testing.T) {
	var c *loginCache
	c.remember("u1", "h", "p")
	assert.False(t, c.matches("u1", "h", "p"))
}

func TestIsAuthUnavailable(t *testing.T) {
	wrapped := fmt.Errorf("%w: timeout", errAuthUnavailable)
	assert.True(t, isAuthUnavailable(wrapped))
	assert.True(t, isAuthUnavailable(gerrors.MultiError{errors.New("no match"), wrapped}))
	assert.False(t, isAuthUnavailable(gerrors.MultiError{errors.New("invalid credentials")}))
	assert.False(t, isAuthUnavailable(errors.New("invalid credentials")))
}

// userDBReturning stubs the login lookup with whatever the user looks like now.
func userDBReturning(current func() (models.User, error)) *mocks.UserDatabase {
	db := &mocks.UserDatabase{}
	db.On("FindOne", mock.Anything, mock.Anything, mock.Anything).Return(&lazyResult{current: current})
	return db
}

// lazyResult decodes whatever the user looks like at the moment of the call,
// so one mock can play a password change between two logins.
type lazyResult struct {
	current func() (models.User, error)
}

func (l *lazyResult) Decode(v interface{}) error {
	u, err := l.current()
	if err != nil {
		return err
	}
	*(v.(*models.User)) = u
	return nil
}

func hashOf(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func withFreshLoginCache(t *testing.T) {
	t.Helper()
	logins = newLoginCache(time.Hour, 100)
	t.Cleanup(func() { logins = nil })
}

// The bug players hit: change your password, and the new one is refused while
// the old one keeps working.
func TestValidateUser_APasswordChangeTakesEffectImmediately(t *testing.T) {
	withFreshLoginCache(t)
	user := models.User{ID: "u1", Details: models.UserDetails{Email: "a@b.com", Password: hashOf(t, "old-password")}}
	m := MiddlewareDB{DB: userDBReturning(func() (models.User, error) { return user, nil })}

	_, err := m.ValidateUser(context.Background(), nil, "a@b.com", "old-password")
	assert.NoError(t, err)

	// Changed on any dyno: all this one sees is the new hash in the database.
	user.Details.Password = hashOf(t, "new-password")

	_, err = m.ValidateUser(context.Background(), nil, "a@b.com", "new-password")
	assert.NoError(t, err, "the new password works straight away")
	_, err = m.ValidateUser(context.Background(), nil, "a@b.com", "old-password")
	assert.Error(t, err, "the old password stops working straight away")
}

// Casing never mattered to the database, and must not matter to the cache.
func TestValidateUser_EmailCasingDoesNotSplitTheCache(t *testing.T) {
	withFreshLoginCache(t)
	user := models.User{ID: "u1", Details: models.UserDetails{Email: "a@b.com", Password: hashOf(t, "pw")}}
	m := MiddlewareDB{DB: userDBReturning(func() (models.User, error) { return user, nil })}

	_, err := m.ValidateUser(context.Background(), nil, "A@B.com", "pw")
	assert.NoError(t, err)
	user.Details.Password = hashOf(t, "pw2")
	_, err = m.ValidateUser(context.Background(), nil, "a@b.com", "pw")
	assert.Error(t, err)
}

// A remembered login must not outlive a suspension or a deactivation.
func TestValidateUser_ModerationReachesAlreadyCachedPlayers(t *testing.T) {
	withFreshLoginCache(t)
	user := models.User{ID: "u1", Details: models.UserDetails{Email: "a@b.com", Password: hashOf(t, "pw")}}
	m := MiddlewareDB{DB: userDBReturning(func() (models.User, error) { return user, nil })}

	_, err := m.ValidateUser(context.Background(), nil, "a@b.com", "pw")
	assert.NoError(t, err)

	user.Details.IsDeactivated = true
	_, err = m.ValidateUser(context.Background(), nil, "a@b.com", "pw")
	assert.ErrorContains(t, err, "deactivated")
}

// A database that cannot answer is not a wrong password.
func TestValidateUser_DatabaseTroubleIsNotAWrongPassword(t *testing.T) {
	withFreshLoginCache(t)
	m := MiddlewareDB{DB: userDBReturning(func() (models.User, error) {
		return models.User{}, context.DeadlineExceeded
	})}
	_, err := m.ValidateUser(context.Background(), nil, "a@b.com", "pw")
	assert.True(t, isAuthUnavailable(err))

	m = MiddlewareDB{DB: userDBReturning(func() (models.User, error) {
		return models.User{}, mongo.ErrNoDocuments
	})}
	_, err = m.ValidateUser(context.Background(), nil, "a@b.com", "pw")
	assert.Error(t, err)
	assert.False(t, isAuthUnavailable(err), "no such account is a real refusal")
}
