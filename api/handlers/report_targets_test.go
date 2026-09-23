package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	targetCommunityID = "507f1f77bcf86cd799439091"
	targetUserID      = "507f1f77bcf86cd799439092"
	targetOtherUserID = "507f1f77bcf86cd799439093"
	targetAnnID       = "507f1f77bcf86cd799439094"
	targetCommentID   = "507f1f77bcf86cd799439095"
)

func oid(t *testing.T, hex string) (id [12]byte) {
	t.Helper()
	o, err := objectID(hex)
	assert.NoError(t, err)
	copy(id[:], o[:])
	return
}

// stubCollection wires db.Collection(name).FindOne(...).Decode(&v) to write
// the given document, or to fail as "not there".
func stubCollection(db *mocks.DatabaseHelper, name string, write func(interface{}), found bool) {
	coll := &mocks.MongoCollectionHelper{}
	result := &mocks.SingleResultHelper{}
	if found {
		result.On("Decode", mock.Anything).Run(func(a mock.Arguments) { write(a.Get(0)) }).Return(nil)
	} else {
		result.On("Decode", mock.Anything).Return(assert.AnError)
	}
	coll.On("FindOne", mock.Anything, mock.Anything, mock.Anything).Return(result)
	db.On("Collection", name).Return(coll)
}

// The snapshot is the point of the whole change: staff see what was reported
// even after it is edited or deleted, and a reporter cannot invent it.
func TestResolveTarget_SnapshotsTheContentFromTheDatabase(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	stubCollection(db, "communities", func(v interface{}) {
		c := v.(*models.Community)
		c.Details.Name = "Vice City RP"
		c.Details.Description = "slurs all over this bit"
		c.Details.ImageLink = "https://img/logo.png"
		c.Details.OwnerID = targetOtherUserID
	}, true)

	got, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetCommunity, ID: targetCommunityID, Fields: []string{"description", "imageLink"},
	})
	assert.NoError(t, err)

	assert.Equal(t, "Community profile", got.snapshot.Label)
	assert.Equal(t, []models.SnapshotField{
		{Field: "description", Label: "Its description", Value: "slurs all over this bit"},
	}, got.snapshot.Text)
	assert.Equal(t, []string{"https://img/logo.png"}, got.snapshot.ImageURLs)
	// The name was not reported, so it is not copied.
	assert.NotContains(t, got.snapshot.Text, models.SnapshotField{Field: "name", Label: "Its name", Value: "Vice City RP"})
	// A community's profile is its owner's, so the strike lands on them.
	assert.Equal(t, targetOtherUserID, got.snapshot.AuthorID)
	assert.NotZero(t, got.snapshot.CapturedAt)
	assert.Empty(t, got.requiresMembershipOf, "a community profile is public")
}

func TestResolveTarget_NoFieldsMeansTheWholeThing(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	stubCollection(db, "users", func(v interface{}) {
		u := v.(*models.User)
		u.Details.Username = "someone"
		u.Details.ProfilePicture = "https://img/avatar.png"
	}, true)

	got, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetUserProfile, ID: targetUserID,
	})
	assert.NoError(t, err)
	assert.Len(t, got.snapshot.Text, 2, "username and display name")
	assert.Equal(t, []string{"https://img/avatar.png"}, got.snapshot.ImageURLs)
	assert.Equal(t, "someone", got.snapshot.AuthorName)
}

// A comment's strike belongs to whoever wrote the comment, not to whoever owns
// the announcement or the community.
func TestResolveTarget_CommentAuthorIsTheCommenter(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	commentOID, _ := objectID(targetCommentID)
	communityOID, _ := objectID(targetCommunityID)
	authorOID, _ := objectID(targetOtherUserID)
	creatorOID, _ := objectID(targetUserID)

	stubCollection(db, "announcements", func(v interface{}) {
		a := v.(*models.Announcement)
		a.Community = communityOID
		a.Creator = creatorOID
		a.Title = "Training tonight"
		a.Comments = []models.Comment{{ID: commentOID, User: authorOID, Content: "something abusive"}}
	}, true)

	got, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetAnnouncementComment, ID: targetCommentID, ParentID: targetAnnID,
	})
	assert.NoError(t, err)
	assert.Equal(t, "something abusive", got.snapshot.Text[0].Value)
	assert.Equal(t, targetOtherUserID, got.snapshot.AuthorID, "the commenter, not the announcement's creator")
	assert.Equal(t, targetCommunityID, got.requiresMembershipOf)
}

func TestResolveTarget_MissingContentIsNotAnError_ToReportOn(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	stubCollection(db, "communities", nil, false)
	_, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetCommunity, ID: targetCommunityID,
	})
	assert.ErrorIs(t, err, errTargetGone)

	// A comment that has since been deleted, on an announcement that still
	// exists, is the same answer.
	db2 := &mocks.DatabaseHelper{}
	stubCollection(db2, "announcements", func(v interface{}) {
		a := v.(*models.Announcement)
		a.Comments = nil
	}, true)
	_, err = targetResolver{db: db2}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetAnnouncementComment, ID: targetCommentID, ParentID: targetAnnID,
	})
	assert.ErrorIs(t, err, errTargetGone)
}

func TestResolveTarget_RefusesWhatIsNotReportable(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	for _, target := range []models.ReportTarget{
		{Kind: "arrest_report", ID: targetCommunityID},
		{Kind: "", ID: targetCommunityID},
		{Kind: models.TargetCommunity, ID: targetCommunityID, Fields: []string{"ownerID"}},
	} {
		_, err := targetResolver{db: db}.resolveTarget(context.Background(), target)
		assert.Error(t, err, "%+v", target)
	}
}

// Nobody can file reports about a community they have never been in.
func TestReporterCanSee_MembersOnlyContent(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"approved member", "approved", true},
		{"still pending", "pending", false},
		{"declined", "declined", false},
		{"banned", "banned", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := &mocks.DatabaseHelper{}
			stubCollection(db, "users", func(v interface{}) {
				u := v.(*models.User)
				u.Details.Communities = []models.UserCommunity{{CommunityID: targetCommunityID, Status: tt.status}}
			}, true)
			got := targetResolver{db: db}.reporterCanSee(context.Background(), targetUserID, targetCommunityID)
			assert.Equal(t, tt.want, got)
		})
	}

	// Public content needs no membership at all.
	assert.True(t, targetResolver{db: &mocks.DatabaseHelper{}}.reporterCanSee(context.Background(), targetUserID, ""))
}

// A kind offered to reporters with no resolver behind it would be a dead end.
func TestEveryReportableKindResolves(t *testing.T) {
	for name := range models.ReportableKinds() {
		db := &mocks.DatabaseHelper{}
		for _, coll := range []string{"communities", "users", "announcements", "featureRequests", "content_creators",
			"civilians", "vehicles", "firearms"} {
			stubCollection(db, coll, nil, false)
		}
		_, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
			Kind: name, ID: targetCommunityID, ParentID: targetCommunityID,
		})
		// Every kind must get as far as looking the content up, which with
		// these stubs means "gone". Anything else means no resolver.
		assert.ErrorIs(t, err, errTargetGone, "%s has no resolver", name)
	}
}

// A character's name and photo are reportable; the rest of its record, the
// roleplay itself, is nobody's business but the community's.
func TestResolveTarget_SnapshotsARoleplayRecord(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	stubCollection(db, "civilians", func(v interface{}) {
		doc := v.(*bson.M)
		*doc = bson.M{"civilian": bson.M{
			"firstName":         "Something",
			"lastName":          "Vile",
			"image":             "https://img/face.png",
			"userID":            targetOtherUserID,
			"activeCommunityID": targetCommunityID,
			"occupation":        "Paramedic",
		}}
	}, true)

	got, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetCivilian, ID: targetCommunityID, Fields: []string{"lastName", "image"},
	})
	assert.NoError(t, err)

	// Only the fields the reporter pointed at travel with the report. The
	// first name was not one of them, and the occupation is not reportable
	// at all: it is roleplay, and the community polices its own.
	assert.Equal(t, []models.SnapshotField{
		{Field: "lastName", Label: "Their last name", Value: "Vile"},
	}, got.snapshot.Text)
	assert.Equal(t, []string{"https://img/face.png"}, got.snapshot.ImageURLs)

	// The strike lands on whoever made the character, and only its own
	// community's members could have seen it.
	assert.Equal(t, targetOtherUserID, got.snapshot.AuthorID)
	assert.Equal(t, targetCommunityID, got.snapshot.CommunityID)
	assert.Equal(t, targetCommunityID, got.requiresMembershipOf)
}

// All three records share one resolver, so a vehicle must not pick up the
// fields that belong to a character.
func TestResolveTarget_RoleplayRecordsOnlyCarryTheirOwnFields(t *testing.T) {
	db := &mocks.DatabaseHelper{}
	stubCollection(db, "vehicles", func(v interface{}) {
		doc := v.(*bson.M)
		*doc = bson.M{"vehicle": bson.M{"plate": "SLUR123", "model": "Bison", "color": "red"}}
	}, true)

	// A character's field on a vehicle is refused outright rather than
	// quietly dropped, because it means the client is confused about what it
	// is reporting and the snapshot would be misleading either way.
	_, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetVehicle, ID: targetCommunityID, Fields: []string{"plate", "firstName"},
	})
	assert.Error(t, err)

	got, err := targetResolver{db: db}.resolveTarget(context.Background(), models.ReportTarget{
		Kind: models.TargetVehicle, ID: targetCommunityID, Fields: []string{"plate"},
	})
	assert.NoError(t, err)
	assert.Equal(t, []models.SnapshotField{
		{Field: "plate", Label: "Its plate", Value: "SLUR123"},
	}, got.snapshot.Text)
	assert.Equal(t, "Vehicle", got.snapshot.Label)
	assert.Empty(t, got.snapshot.ImageURLs)
}
