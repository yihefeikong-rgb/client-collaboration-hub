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

func TestEventRejectsUnsafePortableBody(t *testing.T) {
	for _, body := range []string{"token=do-not-store", `C:\Users\name\secret.txt`, "/home/name/secret.txt"} {
		event := Event{EventID: 1, TaskID: "T-0001", Type: EventMessageAdded, Actor: "codex", At: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC), Body: body, ExpectedVersion: 0}
		if err := event.Validate("T-0001"); err == nil {
			t.Fatalf("unsafe body %q accepted", body)
		}
	}
}

func TestAgentEventRequiresSubmissionAndPolicyDecision(t *testing.T) {
	event := Event{
		EventID:         1,
		TaskID:          "T-0001",
		Type:            EventMessageAdded,
		Actor:           "cc-haha",
		At:              time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
		Body:            "已开始处理。",
		ExpectedVersion: 1,
		Origin:          EventOriginAgent,
	}
	if err := event.Validate(event.TaskID); err == nil {
		t.Fatal("agent event without provenance accepted")
	}
	event.SubmissionID = "sub-001"
	event.PolicyDecision = "agent_auto_human_final"
	if err := event.Validate(event.TaskID); err != nil {
		t.Fatal(err)
	}
}
