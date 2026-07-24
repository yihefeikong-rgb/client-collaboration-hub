package protocol

import (
	"testing"
	"time"
)

func TestDecodeEventLineStrict(t *testing.T) {
	data := []byte(`{"event_id":1,"task_id":"T-0001","type":"assigned","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":[],"expected_version":0,"target_client":"cc-haha"}`)
	if _, err := DecodeEventLine(data, "T-0001"); err != nil {
		t.Fatal(err)
	}
	for _, line := range [][]byte{
		[]byte(`{"event_id":0,"task_id":"T-0001","type":"assigned","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":[],"expected_version":0}`),
		[]byte(`{"event_id":1,"task_id":"T-0002","type":"assigned","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":[],"expected_version":0}`),
		[]byte(`{"event_id":1,"task_id":"T-0001","type":"unknown","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":[],"expected_version":0}`),
		[]byte(`{"event_id":1,"task_id":"T-0001","type":"message_added","actor":"codex","at":"2026-07-25T00:00:00Z","body":" ","evidence_refs":[],"expected_version":0}`),
		[]byte(`{"event_id":1,"task_id":"T-0001","type":"assigned","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":["E-1","E-1"],"expected_version":0}`),
		[]byte(`{"event_id":1,"task_id":"T-0001","type":"assigned","actor":"codex","at":"2026-07-25T00:00:00Z","body":"","evidence_refs":[],"expected_version":0,"extra":true}`),
	} {
		if _, err := DecodeEventLine(line, "T-0001"); err == nil {
			t.Fatal("invalid event accepted")
		}
	}
}

func TestEventRejectsNonUTCTime(t *testing.T) {
	event := Event{EventID: 1, TaskID: "T-0001", Type: EventAssigned, Actor: "codex", At: time.Now(), ExpectedVersion: 0}
	if err := event.Validate("T-0001"); err == nil {
		t.Fatal("local time accepted")
	}
}
