package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/api"
	"github.com/linesmerrill/police-cad-api/databases/mocks"
	"github.com/linesmerrill/police-cad-api/models"
)

const (
	rdCommunityOwner = "user-community-owner"
	rdPlayer         = "user-player"
	rdOfficer        = "user-officer"
	rdAdmin          = "user-admin"
	rdRecordsKeeper  = "user-records"
	rdSettingsAdmin  = "user-settings"
)

func boolPtr(b bool) *bool { return &b }

// rdCommunity builds a community with the record-deletion setting as given
// (nil = never set) and one member holding each permission of interest. Its
// department carries the deprecated RestrictCivilianRecordDeletion=true, which
// must no longer have any effect.
func rdCommunity(allow *bool) *models.Community {
	role := func(name, member string) models.Role {
		return models.Role{ID: primitive.NewObjectID(), Name: "role " + name, Members: []string{member},
			Permissions: []models.Permission{{Name: name, Enabled: true}}}
	}
	return &models.Community{
		ID: primitive.NewObjectID(),
		Details: models.CommunityDetails{
			OwnerID:                     rdCommunityOwner,
			AllowCivilianRecordDeletion: allow,
			Roles: []models.Role{
				role("administrator", rdAdmin),
				role("manage records", rdRecordsKeeper),
				role("manage community settings", rdSettingsAdmin),
			},
			Departments: []models.Department{{ID: primitive.NewObjectID(), Name: "LSPD", RestrictCivilianRecordDeletion: boolPtr(true)}},
		},
	}
}

func TestCivilianRecordDeletionBlocked(t *testing.T) {
	tests := []struct {
		name      string
		allow     *bool
		owner     string
		requester string
		blocked   bool
	}{
		{"unset allows the owner", nil, rdPlayer, rdPlayer, false},
		{"explicit true allows the owner", boolPtr(true), rdPlayer, rdPlayer, false},
		{"false blocks the character's owner", boolPtr(false), rdPlayer, rdPlayer, true},
		{"false does not block an officer", boolPtr(false), rdPlayer, rdOfficer, false},
		{"false does not block the community owner on their own character", boolPtr(false), rdCommunityOwner, rdCommunityOwner, false},
		{"false does not block an administrator on their own character", boolPtr(false), rdAdmin, rdAdmin, false},
		{"false does not block manage records on their own character", boolPtr(false), rdRecordsKeeper, rdRecordsKeeper, false},
		{"manage community settings alone is not a bypass", boolPtr(false), rdSettingsAdmin, rdSettingsAdmin, true},
		{"anonymous request is never blocked", boolPtr(false), rdPlayer, "", false},
		{"character without an owner is never blocked", boolPtr(false), "", rdPlayer, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.blocked, civilianRecordDeletionBlocked(rdCommunity(tt.allow), tt.owner, tt.requester))
		})
	}
	assert.False(t, civilianRecordDeletionBlocked(nil, rdPlayer, rdPlayer), "no community means no setting")
}

// --- criminal history DELETE -------------------------------------------------

func rdCivilian(ownerID, communityID string, citationID primitive.ObjectID, departmentID string) *models.Civilian {
	return &models.Civilian{
		ID: primitive.NewObjectID(),
		Details: models.CivilianDetails{
			UserID:            ownerID,
			ActiveCommunityID: communityID,
			CriminalHistory:   []models.CriminalHistory{{ID: citationID, DepartmentID: departmentID, Type: "Citation"}},
		},
	}
}

func deleteCriminalHistory(t *testing.T, h Civilian, civ *models.Civilian, citationID primitive.ObjectID, requester string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/civilian/"+civ.ID.Hex()+"/criminal-history/"+citationID.Hex(), nil)
	req = mux.SetURLVars(req, map[string]string{"civilian_id": civ.ID.Hex(), "citation_id": citationID.Hex()})
	if requester != "" {
		req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), requester))
	}
	rr := httptest.NewRecorder()
	http.HandlerFunc(h.DeleteCriminalHistoryHandler).ServeHTTP(rr, req)
	return rr
}

func TestDeleteCriminalHistory_CommunitySetting(t *testing.T) {
	tests := []struct {
		name      string
		allow     *bool
		requester string
		wantCode  int
	}{
		{"default allows the owner", nil, rdPlayer, http.StatusOK},
		{"off blocks the owner", boolPtr(false), rdPlayer, http.StatusForbidden},
		{"off does not block an officer", boolPtr(false), rdOfficer, http.StatusOK},
		{"on allows the owner even though the issuing department has the old flag", boolPtr(true), rdPlayer, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			community := rdCommunity(tt.allow)
			citID := primitive.NewObjectID()
			civ := rdCivilian(rdPlayer, community.ID.Hex(), citID, community.Details.Departments[0].ID.Hex())

			cdb := &mocks.CivilianDatabase{}
			cdb.On("FindOne", mock.Anything, mock.Anything).Return(civ, nil)
			cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
			commDB := &mocks.CommunityDatabase{}
			commDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

			rr := deleteCriminalHistory(t, Civilian{DB: cdb, CommDB: commDB}, civ, citID, tt.requester)

			assert.Equal(t, tt.wantCode, rr.Code, rr.Body.String())
			if tt.wantCode == http.StatusForbidden {
				var body map[string]string
				assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
				assert.Equal(t, "record_deletion_restricted", body["error"])
				assert.Contains(t, body["message"], "General Settings")
				cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
			} else {
				cdb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
			}
			if tt.requester != civ.Details.UserID {
				commDB.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
			}
		})
	}
}

// The old per-department flag blocked every non-bypass user, officers
// included. It must no longer be enforced on its own.
func TestDeleteCriminalHistory_DeprecatedDepartmentFlagIgnored(t *testing.T) {
	community := rdCommunity(nil) // department has RestrictCivilianRecordDeletion=true
	citID := primitive.NewObjectID()
	civ := rdCivilian(rdPlayer, community.ID.Hex(), citID, community.Details.Departments[0].ID.Hex())
	for _, requester := range []string{rdPlayer, rdOfficer} {
		cdb := &mocks.CivilianDatabase{}
		cdb.On("FindOne", mock.Anything, mock.Anything).Return(civ, nil)
		cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		commDB := &mocks.CommunityDatabase{}
		commDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

		rr := deleteCriminalHistory(t, Civilian{DB: cdb, CommDB: commDB}, civ, citID, requester)
		assert.Equal(t, http.StatusOK, rr.Code, requester)
	}
}

// The website has no bearer token and identifies the user with ?userId=. That
// identity can only ever refuse, so it is honoured for the refusal.
func TestDeleteCriminalHistory_WebsiteUserIDQueryIsHonouredForRefusal(t *testing.T) {
	community := rdCommunity(boolPtr(false))
	citID := primitive.NewObjectID()
	civ := rdCivilian(rdPlayer, community.ID.Hex(), citID, "")

	cdb := &mocks.CivilianDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(civ, nil)
	commDB := &mocks.CommunityDatabase{}
	commDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/civilian/x/criminal-history/y?userId="+rdPlayer, nil)
	req = mux.SetURLVars(req, map[string]string{"civilian_id": civ.ID.Hex(), "citation_id": citID.Hex()})
	rr := httptest.NewRecorder()
	http.HandlerFunc(Civilian{DB: cdb, CommDB: commDB}.DeleteCriminalHistoryHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
}

// --- arrest report DELETE ----------------------------------------------------

func TestDeleteArrestReport_CommunitySetting(t *testing.T) {
	tests := []struct {
		name      string
		allow     *bool
		requester string
		wantCode  int
	}{
		{"default allows the arrestee's owner", nil, rdPlayer, http.StatusOK},
		{"off blocks the arrestee's owner", boolPtr(false), rdPlayer, http.StatusForbidden},
		{"off does not block the arresting officer", boolPtr(false), rdOfficer, http.StatusOK},
		{"off does not block manage records", boolPtr(false), rdRecordsKeeper, http.StatusOK},
		{"default allows the officer despite the old department flag", nil, rdOfficer, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			community := rdCommunity(tt.allow)
			owner := rdPlayer
			if tt.requester == rdRecordsKeeper {
				owner = rdRecordsKeeper
			}
			civ := rdCivilian(owner, community.ID.Hex(), primitive.NewObjectID(), "")
			report := &models.ArrestReport{ID: primitive.NewObjectID(), Details: models.ArrestReportDetails{
				Arrestee:          models.Arrestee{ID: civ.ID.Hex(), Name: "John Doe"},
				OfficerID:         rdOfficer,
				ActiveCommunityID: community.ID.Hex(),
				DepartmentID:      community.Details.Departments[0].ID.Hex(),
			}}

			adb := &mocks.ArrestReportDatabase{}
			adb.On("FindOne", mock.Anything, mock.Anything).Return(report, nil)
			adb.On("DeleteOne", mock.Anything, mock.Anything).Return(nil)
			cdb := &mocks.CivilianDatabase{}
			cdb.On("FindOne", mock.Anything, mock.Anything).Return(civ, nil)
			commDB := &mocks.CommunityDatabase{}
			commDB.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)

			req, _ := http.NewRequest(http.MethodDelete, "/api/v1/arrest-report/"+report.ID.Hex(), nil)
			req = mux.SetURLVars(req, map[string]string{"arrest_report_id": report.ID.Hex()})
			req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), tt.requester))
			rr := httptest.NewRecorder()
			http.HandlerFunc(ArrestReport{DB: adb, CDB: cdb, CommDB: commDB}.DeleteArrestReportHandler).ServeHTTP(rr, req)

			assert.Equal(t, tt.wantCode, rr.Code, rr.Body.String())
			if tt.wantCode == http.StatusForbidden {
				assert.Contains(t, rr.Body.String(), "record_deletion_restricted")
				adb.AssertNotCalled(t, "DeleteOne", mock.Anything, mock.Anything)
			} else {
				adb.AssertCalled(t, "DeleteOne", mock.Anything, mock.Anything)
			}
		})
	}
}

// --- community PATCH ---------------------------------------------------------

func patchRecordDeletionSetting(t *testing.T, community *models.Community, actor, body string) (*httptest.ResponseRecorder, *mocks.CommunityDatabase) {
	t.Helper()
	cdb := &mocks.CommunityDatabase{}
	cdb.On("FindOne", mock.Anything, mock.Anything).Return(community, nil)
	cdb.On("UpdateOne", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	audit := &mocks.AuditLogDatabase{}
	audit.On("InsertOne", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	req, _ := http.NewRequest(http.MethodPatch, "/api/v1/community/"+community.ID.Hex(), strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"community_id": community.ID.Hex()})
	if actor != "" {
		req = req.WithContext(api.WithAuthenticatedUserID(req.Context(), actor))
	}
	rr := httptest.NewRecorder()
	http.HandlerFunc(Community{DB: cdb, ALDB: audit}.UpdateCommunityFieldHandler).ServeHTTP(rr, req)
	return rr, cdb
}

func TestCommunityPatch_AllowCivilianRecordDeletionIsAdminOnly(t *testing.T) {
	tests := []struct {
		name     string
		actor    string
		wantCode int
	}{
		{"community owner", rdCommunityOwner, http.StatusOK},
		{"administrator", rdAdmin, http.StatusOK},
		{"manage community settings", rdSettingsAdmin, http.StatusOK},
		{"manage records alone cannot change it", rdRecordsKeeper, http.StatusForbidden},
		{"regular player", rdPlayer, http.StatusForbidden},
		{"anonymous", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr, cdb := patchRecordDeletionSetting(t, rdCommunity(nil), tt.actor, `{"allowCivilianRecordDeletion": false}`)
			assert.Equal(t, tt.wantCode, rr.Code, rr.Body.String())
			if tt.wantCode == http.StatusOK {
				cdb.AssertCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
			} else {
				cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

func TestCommunityPatch_AllowCivilianRecordDeletionRejectsBadShapes(t *testing.T) {
	for _, body := range []string{
		`{"allowCivilianRecordDeletion": "false"}`,
		`{"allowCivilianRecordDeletion": null}`,
		`{"allowCivilianRecordDeletion.x": true}`,
	} {
		t.Run(body, func(t *testing.T) {
			rr, cdb := patchRecordDeletionSetting(t, rdCommunity(nil), rdCommunityOwner, body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			cdb.AssertNotCalled(t, "UpdateOne", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// Other keys on the catch-all keep working without the extra gate.
func TestCommunityPatch_OtherKeysUnaffectedByRecordDeletionGate(t *testing.T) {
	rr, cdb := patchRecordDeletionSetting(t, rdCommunity(nil), "", `{"description": "hello"}`)
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	cdb.AssertNotCalled(t, "FindOne", mock.Anything, mock.Anything)
}

// The setting must round-trip in the community payload clients read, and be
// absent (read as allowed) when never set.
func TestAllowCivilianRecordDeletion_Serialisation(t *testing.T) {
	raw, _ := json.Marshal(models.CommunityDetails{})
	assert.NotContains(t, string(raw), "allowCivilianRecordDeletion")
	assert.True(t, models.CommunityDetails{}.CivilianRecordDeletionAllowed())

	raw, _ = json.Marshal(models.CommunityDetails{AllowCivilianRecordDeletion: boolPtr(false)})
	assert.Contains(t, string(raw), `"allowCivilianRecordDeletion":false`)
}
