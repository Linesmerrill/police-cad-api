package handlers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"

	"github.com/linesmerrill/police-cad-api/api/handlers"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

// hashPassword returns a bcrypt hash for use in stubbed user records.
func hashPassword(t *testing.T, plain string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	return string(h)
}

// stubPVUserFindOne wires UDB.FindOne({_id: uID}) → user document.
func stubPVUserFindOne(mockUserDB *mocks.UserDatabase, uID primitive.ObjectID, user *models.User) {
	mr := &mocks.SingleResultHelper{}
	mr.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
		ptr := args.Get(0).(*models.User)
		*ptr = *user
	}).Return(nil)
	mockUserDB.On("FindOne", mock.Anything, bson.M{"_id": uID}).Return(mr)
}

// stubPVUniquenessNoConflict wires the "is this email taken?" lookup to return ErrNoDocuments.
func stubPVUniquenessNoConflict(mockUserDB *mocks.UserDatabase, newEmail string, uID primitive.ObjectID) {
	mr := &mocks.SingleResultHelper{}
	mr.On("Decode", mock.Anything).Return(mongo.ErrNoDocuments)
	mockUserDB.On("FindOne", mock.Anything, bson.M{
		"user.email": newEmail,
		"_id":        bson.M{"$ne": uID},
	}).Return(mr)
}

func newJSONRequest(t *testing.T, method, body string, urlVars map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return mux.SetURLVars(req, urlVars)
}

// -----------------------------------------------------------------------------
// RequestEmailChangeHandler
// -----------------------------------------------------------------------------

func TestRequestEmailChange_WrongPassword_Returns401(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	stubPVUserFindOne(mockUDB, uID, &models.User{
		Details: models.UserDetails{Email: "old@example.com", Password: hashPassword(t, "correct-password")},
	})

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"newEmail":"new@example.com","currentPassword":"WRONG"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.RequestEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	mockPVDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	mockPVDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestRequestEmailChange_EmailAlreadyInUse_Returns409(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	stubPVUserFindOne(mockUDB, uID, &models.User{
		Details: models.UserDetails{Email: "old@example.com", Password: hashPassword(t, "pw")},
	})

	// The uniqueness lookup returns a different account, indicating conflict.
	mr := &mocks.SingleResultHelper{}
	mr.On("Decode", mock.Anything).Return(nil) // nil error = found
	mockUDB.On("FindOne", mock.Anything, bson.M{
		"user.email": "claimed@example.com",
		"_id":        bson.M{"$ne": uID},
	}).Return(mr)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"newEmail":"claimed@example.com","currentPassword":"pw"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.RequestEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusConflict, rr.Code)
}

func TestRequestEmailChange_HappyPath_StoresCodeAndReturns200(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	stubPVUserFindOne(mockUDB, uID, &models.User{
		Details: models.UserDetails{Email: "old@example.com", Password: hashPassword(t, "pw")},
	})
	stubPVUniquenessNoConflict(mockUDB, "new@example.com", uID)

	// Rate-limit FindOne (no prior row) AND upsert FindOne (no existing row to update) both return ErrNoDocuments.
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return((*models.PendingVerification)(nil), mongo.ErrNoDocuments)
	mockPVDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.PendingVerification")).
		Return(&mocks.InsertOneResultHelper{}, nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"newEmail":"new@example.com","currentPassword":"pw"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.RequestEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockPVDB.AssertCalled(t, "InsertOne", mock.Anything, mock.AnythingOfType("models.PendingVerification"))
}

// When a user comes back after the rate-limit window has passed, the upsert resets
// requestCount to 1 — without this, a stale row from a prior session (requestCount=2+)
// would deny the user their free resend immediately on the next attempt.
func TestRequestEmailChange_StaleRow_ResetsRequestCount(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	stubPVUserFindOne(mockUDB, uID, &models.User{
		Details: models.UserDetails{Email: "old@example.com", Password: hashPassword(t, "pw")},
	})
	stubPVUniquenessNoConflict(mockUDB, "new@example.com", uID)

	staleCreatedAt := primitive.NewDateTimeFromTime(time.Now().Add(-5 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposeEmailChange,
			CreatedAt: staleCreatedAt, RequestCount: 3,
		}, nil)
	mockPVDB.On("UpdateOne", mock.Anything,
		bson.M{"userID": uID, "purpose": models.PurposeEmailChange},
		mock.MatchedBy(func(u interface{}) bool {
			update, ok := u.(bson.M)
			if !ok {
				return false
			}
			if _, hasInc := update["$inc"]; hasInc {
				return false
			}
			set, ok := update["$set"].(bson.M)
			if !ok {
				return false
			}
			rc, ok := set["requestCount"].(int)
			return ok && rc == 1
		}),
	).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"newEmail":"new@example.com","currentPassword":"pw"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.RequestEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

// -----------------------------------------------------------------------------
// ConfirmEmailChangeHandler
// -----------------------------------------------------------------------------

func TestConfirmEmailChange_NoPendingRow_Returns400(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return((*models.PendingVerification)(nil), mongo.ErrNoDocuments)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestConfirmEmailChange_ExpiredCode_DeletesRowAndReturns400(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	expired := primitive.NewDateTimeFromTime(time.Now().Add(-1 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposeEmailChange,
			Code: "123456", NewEmail: "new@example.com", Email: "old@example.com",
			ExpiresAt: expired,
		}, nil)
	mockPVDB.On("DeleteOne", mock.Anything, bson.M{"_id": rowID}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockPVDB.AssertCalled(t, "DeleteOne", mock.Anything, bson.M{"_id": rowID})
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestConfirmEmailChange_WrongCode_IncrementsAttempts(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposeEmailChange,
			Code: "123456", NewEmail: "new@example.com", Email: "old@example.com",
			Attempts: 0, ExpiresAt: future,
		}, nil)
	mockPVDB.On("UpdateOne", mock.Anything, bson.M{"_id": rowID}, bson.M{"$inc": bson.M{"attempts": 1}}).
		Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"999999"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockPVDB.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": rowID}, bson.M{"$inc": bson.M{"attempts": 1}})
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestConfirmEmailChange_MaxAttempts_DeletesRow(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposeEmailChange,
			Code: "123456", Attempts: 5, ExpiresAt: future,
		}, nil)
	mockPVDB.On("DeleteOne", mock.Anything, bson.M{"_id": rowID}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockPVDB.AssertCalled(t, "DeleteOne", mock.Anything, bson.M{"_id": rowID})
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestConfirmEmailChange_HappyPath_AppliesChange(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposeEmailChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposeEmailChange,
			Code: "123456", NewEmail: "new@example.com", Email: "old@example.com",
			Attempts: 0, ExpiresAt: future,
		}, nil)
	stubPVUniquenessNoConflict(mockUDB, "new@example.com", uID)
	mockUDB.On("UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)
	mockPVDB.On("DeleteOne", mock.Anything, bson.M{"_id": rowID}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmEmailChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockUDB.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything)
	mockPVDB.AssertCalled(t, "DeleteOne", mock.Anything, bson.M{"_id": rowID})
}

// -----------------------------------------------------------------------------
// ConfirmPasswordChangeHandler — minimal happy path + short-password reject.
// Email-change tests already cover the shared expiry/attempts/code-mismatch logic.
// -----------------------------------------------------------------------------

func TestConfirmPasswordChange_ShortPassword_Returns400(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456","newPassword":"short"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmPasswordChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockPVDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

func TestConfirmPasswordChange_HappyPath_HashesAndUpdates(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposePasswordChange}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposePasswordChange,
			Code: "123456", Email: "user@example.com", Attempts: 0, ExpiresAt: future,
		}, nil)
	mockUDB.On("UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)
	mockPVDB.On("DeleteOne", mock.Anything, bson.M{"_id": rowID}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "PUT", `{"code":"123456","newPassword":"a-new-password-1"}`, map[string]string{"user_id": uID.Hex()})
	http.HandlerFunc(pv.ConfirmPasswordChangeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockUDB.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything)
	mockPVDB.AssertCalled(t, "DeleteOne", mock.Anything, bson.M{"_id": rowID})
}

// -----------------------------------------------------------------------------
// ForgotPassword (unauthenticated reset) handlers
// -----------------------------------------------------------------------------

// stubUserFindByEmail wires UDB.FindOne({"user.email": email}) → user document (or a decode error).
func stubUserFindByEmail(mockUserDB *mocks.UserDatabase, email string, user *models.User, decodeErr error) {
	mr := &mocks.SingleResultHelper{}
	if decodeErr != nil {
		mr.On("Decode", mock.Anything).Return(decodeErr)
	} else {
		mr.On("Decode", mock.Anything).Run(func(args mock.Arguments) {
			ptr := args.Get(0).(*models.User)
			*ptr = *user
		}).Return(nil)
	}
	mockUserDB.On("FindOne", mock.Anything, bson.M{"user.email": email}).Return(mr)
}

func TestForgotPasswordRequestCode_InvalidEmail_Returns400(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"not-an-email"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordRequestCodeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

// An unknown email must still return 200 (generic) and never store a code — no enumeration.
func TestForgotPasswordRequestCode_UnknownEmail_Returns200_NoStore(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}
	stubUserFindByEmail(mockUDB, "ghost@example.com", nil, mongo.ErrNoDocuments)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"ghost@example.com"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordRequestCodeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockPVDB.AssertNotCalled(t, "InsertOne", mock.Anything, mock.Anything)
	mockPVDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestForgotPasswordRequestCode_HappyPath_StoresCodeAndReturns200(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}
	stubUserFindByEmail(mockUDB, "user@example.com", &models.User{
		ID: uID.Hex(), Details: models.UserDetails{Email: "user@example.com"},
	}, nil)
	// Rate-limit FindOne and upsert FindOne (same filter) both find no prior row.
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposePasswordReset}).
		Return((*models.PendingVerification)(nil), mongo.ErrNoDocuments)
	mockPVDB.On("InsertOne", mock.Anything, mock.AnythingOfType("models.PendingVerification")).
		Return(&mocks.InsertOneResultHelper{}, nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"user@example.com"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordRequestCodeHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockPVDB.AssertCalled(t, "InsertOne", mock.Anything, mock.AnythingOfType("models.PendingVerification"))
}

func TestForgotPasswordReset_ShortPassword_Returns400(t *testing.T) {
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"user@example.com","code":"123456","newPassword":"short"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordResetHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

func TestForgotPasswordReset_NoPendingRow_Returns400(t *testing.T) {
	uID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}
	stubUserFindByEmail(mockUDB, "user@example.com", &models.User{
		ID: uID.Hex(), Details: models.UserDetails{Email: "user@example.com"},
	}, nil)
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposePasswordReset}).
		Return((*models.PendingVerification)(nil), mongo.ErrNoDocuments)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"user@example.com","code":"123456","newPassword":"a-good-password"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordResetHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

func TestForgotPasswordReset_WrongCode_IncrementsAttempts(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}
	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	stubUserFindByEmail(mockUDB, "user@example.com", &models.User{
		ID: uID.Hex(), Details: models.UserDetails{Email: "user@example.com"},
	}, nil)
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposePasswordReset}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposePasswordReset,
			Code: "999999", Email: "user@example.com", Attempts: 0, ExpiresAt: future,
		}, nil)
	mockPVDB.On("UpdateOne", mock.Anything, bson.M{"_id": rowID}, bson.M{"$inc": bson.M{"attempts": 1}}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"user@example.com","code":"123456","newPassword":"a-good-password"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordResetHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	mockUDB.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
	mockPVDB.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": rowID}, bson.M{"$inc": bson.M{"attempts": 1}})
}

func TestForgotPasswordReset_HappyPath_UpdatesPassword(t *testing.T) {
	uID := primitive.NewObjectID()
	rowID := primitive.NewObjectID()
	mockUDB := &mocks.UserDatabase{}
	mockPVDB := &mocks.PendingVerificationDatabase{}
	future := primitive.NewDateTimeFromTime(time.Now().Add(10 * time.Minute))
	stubUserFindByEmail(mockUDB, "user@example.com", &models.User{
		ID: uID.Hex(), Details: models.UserDetails{Email: "user@example.com"},
	}, nil)
	mockPVDB.On("FindOne", mock.Anything, bson.M{"userID": uID, "purpose": models.PurposePasswordReset}).
		Return(&models.PendingVerification{
			ID: rowID, UserID: uID, Purpose: models.PurposePasswordReset,
			Code: "123456", Email: "user@example.com", Attempts: 0, ExpiresAt: future,
		}, nil)
	mockUDB.On("UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything).
		Return(&mongo.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil)
	mockPVDB.On("DeleteOne", mock.Anything, bson.M{"_id": rowID}).Return(nil)

	pv := handlers.PendingVerification{PVDB: mockPVDB, UDB: mockUDB}
	rr := httptest.NewRecorder()
	req := newJSONRequest(t, "POST", `{"email":"user@example.com","code":"123456","newPassword":"a-good-password"}`, nil)
	http.HandlerFunc(pv.ForgotPasswordResetHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockUDB.AssertCalled(t, "UpdateOne", mock.Anything, bson.M{"_id": uID}, mock.Anything)
	mockPVDB.AssertCalled(t, "DeleteOne", mock.Anything, bson.M{"_id": rowID})
}
