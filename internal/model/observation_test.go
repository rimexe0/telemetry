package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizePreservesOriginalAndCanonicalizesProjection(t *testing.T) {
	original := json.RawMessage([]byte("{ \"second\": 2, \"first\": 1 }"))
	o := Observation{
		SourceInstanceKey: "activitywatch:mac:window",
		SourceKind:        "activitywatch",
		DeviceID:          "mac",
		SourceRecordKey:   "42",
		Kind:              "activitywatch.event",
		OccurredAt:        time.Date(2026, 9, 7, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*3600)),
		CollectedAt:       time.Date(2026, 9, 7, 12, 1, 0, 0, time.UTC),
		OriginalPayload:   original,
	}
	if err := o.Normalize(); err != nil {
		t.Fatal(err)
	}
	if string(o.OriginalPayload) != string(original) {
		t.Fatalf("original payload changed: %s", o.OriginalPayload)
	}
	if got, want := string(o.Projection), "{\"first\":1,\"second\":2}"; got != want {
		t.Fatalf("projection = %s, want %s", got, want)
	}
	if len(o.RevisionDigest) != 64 {
		t.Fatalf("unexpected digest %q", o.RevisionDigest)
	}
	if o.OccurredAt.Location() != time.UTC {
		t.Fatalf("occurred_at location = %s", o.OccurredAt.Location())
	}
}

func TestNormalizeRejectsInvalidRequiredFields(t *testing.T) {
	o := Observation{OriginalPayload: json.RawMessage([]byte("{}"))}
	err := o.Normalize()
	if err == nil || !strings.Contains(err.Error(), "source_instance_key") {
		t.Fatalf("expected missing source instance error, got %v", err)
	}
}

func TestQueueItemIDChangesForNewRevision(t *testing.T) {
	base := Observation{SourceInstanceKey: "source", SourceRecordKey: "record", RevisionDigest: strings.Repeat("a", 64)}
	changed := base
	changed.RevisionDigest = strings.Repeat("b", 64)
	if QueueItemID(base) == QueueItemID(changed) {
		t.Fatal("revision must change the queue identity")
	}
}

func TestDeliveryBatchIDIgnoresClaimOrder(t *testing.T) {
	first, err := DeliveryBatchID([]string{"queue-b", "queue-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := DeliveryBatchID([]string{"queue-a", "queue-b"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("batch IDs differ: %s vs %s", first, second)
	}
}
