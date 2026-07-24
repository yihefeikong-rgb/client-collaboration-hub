package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type EventType string

const (
	EventTaskCreated      EventType = "task_created"
	EventAssigned         EventType = "assigned"
	EventAccepted         EventType = "accepted"
	EventMessageAdded     EventType = "message_added"
	EventEvidenceAdded    EventType = "evidence_added"
	EventSubmitted        EventType = "submitted"
	EventChangesRequested EventType = "changes_requested"
	EventRevisionStarted  EventType = "revision_started"
	EventApproved         EventType = "approved"
	EventBlocked          EventType = "blocked"
)

type Event struct {
	EventID         int64     `json:"event_id"`
	TaskID          string    `json:"task_id"`
	Type            EventType `json:"type"`
	Actor           string    `json:"actor"`
	At              time.Time `json:"at"`
	Body            string    `json:"body"`
	EvidenceRefs    []string  `json:"evidence_refs"`
	ExpectedVersion int64     `json:"expected_version"`
}

func (e Event) Validate(taskID string) error {
	if e.EventID < 1 {
		return fmt.Errorf("event_id must be positive")
	}
	if e.TaskID != taskID || !IsValidID(e.TaskID) {
		return fmt.Errorf("event task_id %q does not match %q", e.TaskID, taskID)
	}
	if !IsValidID(e.Actor) {
		return fmt.Errorf("invalid event actor %q", e.Actor)
	}
	if err := validateUTCTime("event at", e.At); err != nil {
		return err
	}
	if !knownEventType(e.Type) {
		return fmt.Errorf("unknown event type %q", e.Type)
	}
	if eventNeedsBody(e.Type) && strings.TrimSpace(e.Body) == "" {
		return fmt.Errorf("event type %q requires body", e.Type)
	}
	if e.ExpectedVersion < 0 {
		return fmt.Errorf("event expected_version must not be negative")
	}
	seen := map[string]bool{}
	for _, ref := range e.EvidenceRefs {
		if !IsValidID(ref) || seen[ref] {
			return fmt.Errorf("invalid or duplicate evidence reference %q", ref)
		}
		seen[ref] = true
	}
	return nil
}

func DecodeEventLine(data []byte, taskID string) (Event, error) {
	var event Event
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return event, fmt.Errorf("decode event: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return event, fmt.Errorf("event line contains multiple JSON values")
		}
		return event, fmt.Errorf("decode extra event value: %w", err)
	}
	if err := event.Validate(taskID); err != nil {
		return event, err
	}
	return event, nil
}

func knownEventType(eventType EventType) bool {
	switch eventType {
	case EventTaskCreated, EventAssigned, EventAccepted, EventMessageAdded, EventEvidenceAdded, EventSubmitted, EventChangesRequested, EventRevisionStarted, EventApproved, EventBlocked:
		return true
	default:
		return false
	}
}

func eventNeedsBody(eventType EventType) bool {
	switch eventType {
	case EventTaskCreated, EventMessageAdded, EventEvidenceAdded, EventChangesRequested, EventBlocked:
		return true
	default:
		return false
	}
}
