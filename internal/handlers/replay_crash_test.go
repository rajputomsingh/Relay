package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rajputomsingh/relay/internal/lifecycle"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestReplayStaleClaimRecoversAfter30Seconds simulates a replay handler
// that crashed after claiming but before releasing the claim. A next
// request within 30 seconds is rejected (claim still fresh); after 30
// seconds, the stale claim is recovered and the next request wins.
func TestReplayStaleClaimRecoversAfter30Seconds(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_stale_claim", 1, 1)
	ctx := context.Background()

	if _, err := f.events.UpdateOne(
		ctx,
		bson.M{"_id": f.eventID},
		bson.M{"$set": bson.M{
			"replay_in_flight":  true,
			"replay_claimed_at": time.Now().UTC().Add(-31 * time.Second),
		}},
	); err != nil {
		t.Fatalf("seed stale claim: %v", err)
	}

	rec := f.serveReplay(t)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 (stale claim recovered), got %d: %s", rec.Code, rec.Body.String())
	}

	var parent bson.M
	if err := f.events.FindOne(ctx, bson.M{"_id": f.eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if _, present := parent["replay_in_flight"]; present {
		t.Errorf("replay_in_flight should be released, got %v", parent["replay_in_flight"])
	}
}

// TestReplayFreshClaimRejectsSecondRequest confirms that while a replay
// claim is active (fresh, less than 30s old), a second request gets 409.
func TestReplayFreshClaimRejectsSecondRequest(t *testing.T) {
	f := newReplayFixture(t, "relay_replay_fresh_claim", 1, 1)
	ctx := context.Background()

	if _, err := f.events.UpdateOne(
		ctx,
		bson.M{"_id": f.eventID},
		bson.M{"$set": bson.M{
			"replay_in_flight":  true,
			"replay_claimed_at": time.Now().UTC().Add(-5 * time.Second),
		}},
	); err != nil {
		t.Fatalf("seed fresh claim: %v", err)
	}

	rec := f.serveReplay(t)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	var parent bson.M
	if err := f.events.FindOne(ctx, bson.M{"_id": f.eventID}).Decode(&parent); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if got, _ := parent["delivery_status"].(string); got != lifecycle.StatusFailed {
		t.Errorf("status = %q; want failed (unchanged)", got)
	}
}
