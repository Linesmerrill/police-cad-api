package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// Withdrawing a key has to stop its deliveries, not just its requests.
//
// This is the case that is easy to get wrong, because the endpoint's own status
// is still "active" and its community grants may still be active too. The only
// thing that changed is the key, and if the worker does not re-read it then a
// revoked integration carries on being told everything that happens in a
// community it is no longer allowed into.

// ---- fakes ----

type fakeHookDB struct {
	databases.DeveloperWebhookDatabase
	hooks   []models.DeveloperWebhook
	updates []bson.M
}

func (f *fakeHookDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) ([]models.DeveloperWebhook, error) {
	return append([]models.DeveloperWebhook{}, f.hooks...), nil
}

func (f *fakeHookDB) UpdateOne(_ context.Context, _, update interface{}, _ ...*options.UpdateOptions) (*mongo.UpdateResult, error) {
	if m, ok := update.(bson.M); ok {
		f.updates = append(f.updates, m)
	}
	return &mongo.UpdateResult{ModifiedCount: 1}, nil
}

type fakeHookEventDB struct {
	databases.DeveloperEventDatabase
	events []models.DeveloperAPIEvent
}

func (f *fakeHookEventDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) ([]models.DeveloperAPIEvent, error) {
	return append([]models.DeveloperAPIEvent{}, f.events...), nil
}

type fakeHookGrantDB struct {
	databases.DeveloperGrantDatabase
	grants []models.DeveloperCommunityGrant
}

func (f *fakeHookGrantDB) Find(_ context.Context, _ interface{}, _ ...*options.FindOptions) (*databases.MongoCursor, error) {
	docs := make([]interface{}, 0, len(f.grants))
	for _, g := range f.grants {
		docs = append(docs, g)
	}
	cur, err := databases.NewMongoCursorFromDocuments(docs)
	if err != nil {
		return nil, err
	}
	return &cur, nil
}

type fakeHookKeyDB struct {
	databases.DeveloperAPIKeyDatabase
	key *models.DeveloperAPIKey
}

func (f *fakeHookKeyDB) FindOne(_ context.Context, _ interface{}) (*models.DeveloperAPIKey, error) {
	if f.key == nil {
		return nil, mongo.ErrNoDocuments
	}
	found := *f.key
	return &found, nil
}

type fakeHookAccountDB struct {
	databases.DeveloperAccountDatabase
	account *models.DeveloperAccount
}

func (f *fakeHookAccountDB) FindOne(_ context.Context, _ interface{}) (*models.DeveloperAccount, error) {
	if f.account == nil {
		return nil, mongo.ErrNoDocuments
	}
	found := *f.account
	return &found, nil
}

type fakeHookDeliveryDB struct {
	databases.DeveloperWebhookDeliveryDatabase
	rows []models.DeveloperWebhookDelivery
}

func (f *fakeHookDeliveryDB) InsertOne(_ context.Context, row models.DeveloperWebhookDelivery) (databases.InsertOneResultHelper, error) {
	f.rows = append(f.rows, row)
	return nil, nil
}

// ---- the stack ----

type deliveryFixture struct {
	s         *Scheduler
	hookDB    *fakeHookDB
	eventDB   *fakeHookEventDB
	grantDB   *fakeHookGrantDB
	keyDB     *fakeHookKeyDB
	accountDB *fakeHookAccountDB
	// received counts the requests the receiver actually got, which is the only
	// thing these tests really assert on.
	received *int
	server   *httptest.Server
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()

	received := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	communityID := primitive.NewObjectID().Hex()
	keyID := primitive.NewObjectID()
	developerID := primitive.NewObjectID()

	hookDB := &fakeHookDB{hooks: []models.DeveloperWebhook{{
		ID:          primitive.NewObjectID(),
		KeyID:       keyID.Hex(),
		DeveloperID: developerID.Hex(),
		// The real gate refuses an http loopback URL, which is exactly what a
		// test server is. The address check is covered by its own tests; what
		// this fixture exercises is everything that happens before it, so the
		// assertions are on whether a request was attempted at all.
		URL:        server.URL,
		SecretHash: "deadbeef",
		Status:     models.WebhookStatusActive,
	}}}
	eventDB := &fakeHookEventDB{events: []models.DeveloperAPIEvent{{
		ID:          primitive.NewObjectID(),
		Type:        models.EventCallCreated,
		CommunityID: communityID,
		At:          primitive.NewDateTimeFromTime(time.Now()),
	}}}
	grantDB := &fakeHookGrantDB{grants: []models.DeveloperCommunityGrant{{
		KeyID: keyID.Hex(), CommunityID: communityID, Status: models.DeveloperKeyActive,
	}}}
	keyDB := &fakeHookKeyDB{key: &models.DeveloperAPIKey{
		ID: keyID, DeveloperID: developerID.Hex(), Status: models.DeveloperKeyActive,
	}}
	accountDB := &fakeHookAccountDB{account: &models.DeveloperAccount{
		ID: developerID, Status: models.DeveloperAccountActive,
	}}

	s := &Scheduler{instanceID: "test", jobStats: map[string]*JobStat{}}
	s.WithDeveloperWebhooks(hookDB, &fakeHookDeliveryDB{}, eventDB, grantDB, keyDB, accountDB)

	return &deliveryFixture{
		s: s, hookDB: hookDB, eventDB: eventDB, grantDB: grantDB,
		keyDB: keyDB, accountDB: accountDB, received: &received, server: server,
	}
}

// ---- the tests ----

// ---- the tests ----

// A live key, an active endpoint and a granted community: the worker gets past
// every pre-flight check and as far as the address gate.
//
// This is the control, and it has to be a sharp one or the tests below pass
// because the fixture never delivers anything at all. The distinction asserted
// is between "stopped before trying" and "tried": a live key reaches the
// address gate and is refused by it (the receiver is a loopback test server,
// which that gate exists to refuse), so it comes back with an error. A key that
// should not deliver comes back with nil and no attempt. Those two outcomes
// cannot be confused.
func TestDeliverForWebhook_LiveKeyReachesTheAddressGate(t *testing.T) {
	f := newDeliveryFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0])

	if err == nil {
		t.Fatal("a loopback receiver should be refused by the address gate, " +
			"so a live key reaching it must return an error")
	}
	// The test server is http, so https is the first thing the gate objects
	// to. Either of its refusals proves the worker got that far, which is the
	// property being asserted.
	fromGate := strings.Contains(err.Error(), "must be https") ||
		strings.Contains(err.Error(), "public internet")
	if !fromGate {
		t.Fatalf("expected the address gate to be what refused it, got %v", err)
	}
	if sent != 0 {
		t.Errorf("nothing should have been delivered to a loopback address, sent %d", sent)
	}
	// Belt and braces: the gate stops it before any request is made, so the
	// test server is never called even in the control.
	if *f.received != 0 {
		t.Errorf("the address gate must stop the request, got %d", *f.received)
	}
}

// Revoking a key stops its deliveries. The endpoint is still active and the
// grant is still active; only the key changed.
func TestDeliverForWebhook_RevokedKeyStopsDeliveries(t *testing.T) {
	f := newDeliveryFixture(t)
	f.keyDB.key.Status = models.DeveloperKeyRevoked

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0])

	if err != nil {
		t.Fatalf("a revoked key is not an error, just nothing to do: %v", err)
	}
	if sent != 0 {
		t.Errorf("a revoked key must deliver nothing, sent %d", sent)
	}
	if *f.received != 0 {
		t.Errorf("the receiver must not be called for a revoked key, got %d requests", *f.received)
	}
	// The cursor is left alone, so reinstating the key resumes rather than
	// having silently skipped everything that happened while it was off.
	for _, update := range f.hookDB.updates {
		if set, ok := update["$set"].(bson.M); ok {
			if _, moved := set["cursor"]; moved {
				t.Error("a revoked key must not advance the endpoint's cursor")
			}
		}
	}
}

// A key that no longer exists at all delivers nothing either.
func TestDeliverForWebhook_DeletedKeyStopsDeliveries(t *testing.T) {
	f := newDeliveryFixture(t)
	f.keyDB.key = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0]); sent != 0 || err != nil {
		t.Errorf("sent=%d err=%v", sent, err)
	}
	if *f.received != 0 {
		t.Error("the receiver must not be called for a key that no longer exists")
	}
}

// Suspending a developer stops every key they hold, on the request path and
// here. The asymmetry would have been a latent bug of the same shape.
func TestDeliverForWebhook_SuspendedDeveloperStopsDeliveries(t *testing.T) {
	f := newDeliveryFixture(t)
	f.accountDB.account.Status = models.DeveloperAccountSuspended

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0]); sent != 0 || err != nil {
		t.Errorf("sent=%d err=%v", sent, err)
	}
	if *f.received != 0 {
		t.Error("the receiver must not be called for a suspended developer")
	}
}

// A community owner withdrawing their grant stops deliveries about their
// community, and the cursor advances past the event so the endpoint does not
// stall on something it will never be allowed to receive.
func TestDeliverForWebhook_WithdrawnGrantStopsThatCommunity(t *testing.T) {
	f := newDeliveryFixture(t)
	f.grantDB.grants = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent != 0 || *f.received != 0 {
		t.Errorf("an ungranted community must not be delivered: sent=%d received=%d", sent, *f.received)
	}

	advanced := false
	for _, update := range f.hookDB.updates {
		if set, ok := update["$set"].(bson.M); ok {
			if _, moved := set["cursor"]; moved {
				advanced = true
			}
		}
	}
	if !advanced {
		t.Error("the cursor must move past an event this endpoint may never receive, or it stalls")
	}
}

// Without a key store the worker refuses rather than delivering. A missing
// store means "cannot prove the key is live", and the safe reading of that is
// to stop.
func TestDeliverForWebhook_NoKeyStoreFailsClosed(t *testing.T) {
	f := newDeliveryFixture(t)
	f.s.KeyDB = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sent, err := f.s.deliverForWebhook(ctx, f.hookDB.hooks[0])
	if err == nil {
		t.Error("an unprovable key must stop deliveries rather than allow them")
	}
	if sent != 0 || *f.received != 0 {
		t.Errorf("nothing should have been delivered: sent=%d received=%d", sent, *f.received)
	}
}
