package handlers

import (
	"testing"
	"time"

	"github.com/linesmerrill/police-cad-api/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestStatusUpdateStampsReleasedAt(t *testing.T) {
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	nowDT := primitive.NewDateTimeFromTime(now)

	// open -> released stamps releasedAt.
	u := statusUpdate(&models.FeatureRequest{Status: "open"}, "released", now)
	set := u["$set"].(bson.M)
	if set["releasedAt"] != nowDT {
		t.Fatalf("releasedAt = %v, want %v", set["releasedAt"], nowDT)
	}
	if _, ok := u["$unset"]; ok {
		t.Fatal("released must not unset releasedAt")
	}

	// Re-saving released keeps the original release date.
	earlier := primitive.NewDateTimeFromTime(now.Add(-48 * time.Hour))
	u = statusUpdate(&models.FeatureRequest{Status: "released", ReleasedAt: &earlier}, "released", now)
	if _, ok := u["$set"].(bson.M)["releasedAt"]; ok {
		t.Fatal("re-saving released must not move releasedAt")
	}

	// Released before releasedAt existed: stamp it now.
	u = statusUpdate(&models.FeatureRequest{Status: "released"}, "released", now)
	if u["$set"].(bson.M)["releasedAt"] != nowDT {
		t.Fatal("legacy released request should get releasedAt")
	}

	// Leaving released clears it.
	u = statusUpdate(&models.FeatureRequest{Status: "released", ReleasedAt: &earlier}, "beta_testing", now)
	if u["$unset"].(bson.M)["releasedAt"] != "" {
		t.Fatal("moving off released must unset releasedAt")
	}
	if u["$set"].(bson.M)["status"] != "beta_testing" {
		t.Fatal("status not set")
	}
}
