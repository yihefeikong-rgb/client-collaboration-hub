package protocol

import (
	"fmt"
	"strings"
)

type EvidenceResolver interface {
	ResolveEvidence(taskID, evidenceID string) (Evidence, error)
}

func Replay(task Task, events []Event, refs References, resolver EvidenceResolver) (State, error) {
	return ReplayWithPolicy(task, events, refs, resolver, DefaultActionPolicy{})
}

func ReplayWithPolicy(task Task, events []Event, refs References, resolver EvidenceResolver, policy ActionPolicy) (State, error) {
	if refs == nil || resolver == nil || policy == nil {
		return State{}, fmt.Errorf("replay requires references and evidence resolver")
	}
	if err := task.Validate(task.ID, refs); err != nil {
		return State{}, err
	}
	if len(events) == 0 {
		return State{}, fmt.Errorf("task_created event is missing")
	}
	var state State
	announcedEvidence := map[string]bool{}
	for index, event := range events {
		if err := event.Validate(task.ID); err != nil {
			return State{}, err
		}
		if event.EventID != int64(index+1) {
			return State{}, fmt.Errorf("event_id is not contiguous")
		}
		if index == 0 {
			if err := replayTaskCreated(task, event); err != nil {
				return State{}, err
			}
			state = State{TaskID: task.ID, Status: Draft, Version: 1, LastEventID: 1, ResponsibleClient: task.Creator, UpdatedAt: task.CreatedAt}
			continue
		}
		if event.ExpectedVersion != state.Version || event.At.Before(state.UpdatedAt) {
			return State{}, fmt.Errorf("event version or time does not follow state")
		}
		if !refs.ClientExists(event.Actor) {
			return State{}, fmt.Errorf("event actor %q is not registered", event.Actor)
		}
		var err error
		switch event.Type {
		case EventMessageAdded:
			if err := policy.Authorize(task, state, event.Actor, Message, refs); err != nil || len(event.EvidenceRefs) != 0 {
				return State{}, fmt.Errorf("invalid message event")
			}
			state = advanceNonBusiness(state, event)
		case EventEvidenceAdded:
			if err := policy.Authorize(task, state, event.Actor, AddEvidence, refs); err != nil {
				return State{}, err
			}
			if err := validateEvidenceAdded(event, resolver, announcedEvidence); err != nil {
				return State{}, err
			}
			announcedEvidence[event.EvidenceRefs[0]] = true
			state = advanceNonBusiness(state, event)
		default:
			state, err = replayTransition(state, task, event, refs, resolver, announcedEvidence, policy)
			if err != nil {
				return State{}, err
			}
		}
		if state.Version != event.EventID || state.LastEventID != event.EventID || !state.UpdatedAt.Equal(event.At) {
			return State{}, fmt.Errorf("replayed state does not match event")
		}
	}
	return state, nil
}

func replayTaskCreated(task Task, event Event) error {
	if event.Type != EventTaskCreated || event.EventID != 1 || event.ExpectedVersion != 0 || event.Actor != task.Creator || !event.At.Equal(task.CreatedAt) || event.Body != task.Title || len(event.EvidenceRefs) != 0 {
		return fmt.Errorf("invalid task_created event")
	}
	return nil
}

func replayTransition(state State, task Task, event Event, refs References, resolver EvidenceResolver, announcedEvidence map[string]bool, policy ActionPolicy) (State, error) {
	action, err := actionForEvent(event.Type)
	if err != nil {
		return State{}, err
	}
	if err := policy.Authorize(task, state, event.Actor, action, refs); err != nil {
		return State{}, err
	}
	if event.Type != EventSubmitted && event.Type != EventBlocked && len(event.EvidenceRefs) != 0 {
		return State{}, fmt.Errorf("event type %q must not include evidence", event.Type)
	}
	if event.Type != EventChangesRequested && strings.TrimSpace(event.Body) != "" {
		return State{}, fmt.Errorf("event type %q must not include body", event.Type)
	}
	if event.Type == EventAssigned {
		if err := ValidateAssignmentTarget(event.TargetClient, refs); err != nil {
			return State{}, err
		}
	}
	kinds, err := evidenceKinds(event.EvidenceRefs, task.ID, resolver, announcedEvidence)
	if err != nil {
		return State{}, err
	}
	request := TransitionRequest{Action: action, Actor: event.Actor, At: event.At, EvidenceKinds: kinds}
	switch event.Type {
	case EventAssigned:
		request.NextAssignee = event.TargetClient
	case EventChangesRequested:
		request.Feedback = event.Body
	}
	return Transition(state, task, request)
}

func actionForEvent(eventType EventType) (Action, error) {
	switch eventType {
	case EventAssigned:
		return Assign, nil
	case EventAccepted:
		return Accept, nil
	case EventSubmitted:
		return Submit, nil
	case EventChangesRequested:
		return RequestChanges, nil
	case EventRevisionStarted:
		return Resume, nil
	case EventApproved:
		return Approve, nil
	case EventBlocked:
		return Block, nil
	default:
		return "", fmt.Errorf("event type %q is not a transition", eventType)
	}
}

// ValidateAssignmentTarget verifies the durable registry reference required by
// an assigned event. It is shared by write-time validation and Replay.
func ValidateAssignmentTarget(target string, refs References) error {
	if refs == nil || !IsValidID(target) || !refs.ClientExists(target) {
		return fmt.Errorf("assigned client is not registered")
	}
	if !refs.ClientHasCapability(target, "execute") {
		return fmt.Errorf("assigned client lacks execute capability")
	}
	return nil
}

func validateEvidenceAdded(event Event, resolver EvidenceResolver, announcedEvidence map[string]bool) error {
	if len(event.EvidenceRefs) != 1 {
		return fmt.Errorf("evidence_added requires exactly one evidence reference")
	}
	if announcedEvidence[event.EvidenceRefs[0]] {
		return fmt.Errorf("evidence %q was already announced", event.EvidenceRefs[0])
	}
	evidence, err := resolver.ResolveEvidence(event.TaskID, event.EvidenceRefs[0])
	if err != nil {
		return err
	}
	if evidence.ID != event.EvidenceRefs[0] || evidence.TaskID != event.TaskID || evidence.CreatedBy != event.Actor || evidence.Summary != event.Body || !evidence.CreatedAt.Equal(event.At) {
		return fmt.Errorf("evidence_added event does not match evidence")
	}
	return nil
}

func evidenceKinds(refs []string, taskID string, resolver EvidenceResolver, announcedEvidence map[string]bool) ([]EvidenceKind, error) {
	seen := map[string]bool{}
	kinds := make([]EvidenceKind, 0, len(refs))
	for _, id := range refs {
		if seen[id] {
			return nil, fmt.Errorf("duplicate evidence reference %q", id)
		}
		seen[id] = true
		if !announcedEvidence[id] {
			return nil, fmt.Errorf("evidence %q has not been announced", id)
		}
		evidence, err := resolver.ResolveEvidence(taskID, id)
		if err != nil {
			return nil, err
		}
		if evidence.ID != id || evidence.TaskID != taskID {
			return nil, fmt.Errorf("evidence %q does not match task", id)
		}
		kinds = append(kinds, evidence.Kind)
	}
	return kinds, nil
}

func advanceNonBusiness(state State, event Event) State {
	state.Version++
	state.LastEventID++
	state.UpdatedAt = event.At
	return state
}
