package protocol

import (
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	Draft            Status = "DRAFT"
	Assigned         Status = "ASSIGNED"
	Working          Status = "WORKING"
	Review           Status = "REVIEW"
	RevisionRequired Status = "REVISION_REQUIRED"
	Done             Status = "DONE"
	Blocked          Status = "BLOCKED"
)

type EvidenceKind string

const (
	EvidenceDiff     EvidenceKind = "diff"
	EvidenceArtifact EvidenceKind = "artifact"
	EvidenceTest     EvidenceKind = "test"
	EvidenceBlocker  EvidenceKind = "blocker"
)

type State struct {
	TaskID            string    `json:"task_id"`
	Status            Status    `json:"status"`
	Version           int64     `json:"version"`
	LastEventID       int64     `json:"last_event_id"`
	AssignedClient    string    `json:"assigned_client"`
	ResponsibleClient string    `json:"responsible_client"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Action string

const (
	Assign         Action = "assign"
	Accept         Action = "accept"
	Submit         Action = "submit"
	RequestChanges Action = "request_changes"
	Resume         Action = "resume"
	Approve        Action = "approve"
	Block          Action = "block"
)

type TransitionRequest struct {
	Action        Action
	Actor         string
	NextAssignee  string
	Feedback      string
	EvidenceKinds []EvidenceKind
	At            time.Time
}

func (s State) Validate(task Task) error {
	if s.TaskID != task.ID {
		return fmt.Errorf("state task_id does not match task")
	}
	if !isBusinessStatus(s.Status) {
		return fmt.Errorf("invalid business status %q", s.Status)
	}
	if s.Version < 0 || s.LastEventID < 0 {
		return fmt.Errorf("state version and last_event_id must not be negative")
	}
	if err := validateUTCTime("state updated_at", s.UpdatedAt); err != nil {
		return err
	}
	switch s.Status {
	case Draft:
		if s.AssignedClient != "" || s.ResponsibleClient != task.Creator {
			return fmt.Errorf("DRAFT requires empty assigned_client and creator responsible_client")
		}
	case Assigned, Working:
		if !IsValidID(s.AssignedClient) || s.ResponsibleClient != s.AssignedClient {
			return fmt.Errorf("%s requires assigned client to be responsible", s.Status)
		}
	case Review, Done:
		if !IsValidID(s.AssignedClient) || s.ResponsibleClient != task.Reviewer {
			return fmt.Errorf("%s requires assigned client and reviewer responsible_client", s.Status)
		}
	case RevisionRequired:
		if !IsValidID(s.AssignedClient) || s.ResponsibleClient != s.AssignedClient {
			return fmt.Errorf("REVISION_REQUIRED requires assigned client to be responsible")
		}
	case Blocked:
		if !IsValidID(s.AssignedClient) || !isAllowedResponsible(s.ResponsibleClient, task, s.AssignedClient) {
			return fmt.Errorf("BLOCKED requires retained assigned and responsible clients")
		}
	}
	return nil
}

func Transition(state State, task Task, request TransitionRequest) (State, error) {
	if err := state.Validate(task); err != nil {
		return State{}, err
	}
	if !IsValidID(request.Actor) {
		return State{}, fmt.Errorf("invalid transition actor %q", request.Actor)
	}
	if err := validateUTCTime("transition time", request.At); err != nil {
		return State{}, err
	}
	if err := validateEvidenceKinds(request.EvidenceKinds); err != nil {
		return State{}, err
	}
	result := state
	switch request.Action {
	case Assign:
		if request.Actor != task.Creator || (state.Status != Draft && state.Status != Blocked) || !IsValidID(request.NextAssignee) {
			return State{}, fmt.Errorf("assign requires creator, DRAFT or BLOCKED, and valid next assignee")
		}
		result.Status, result.AssignedClient, result.ResponsibleClient = Assigned, request.NextAssignee, request.NextAssignee
	case Accept:
		if state.Status != Assigned || request.Actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("accept requires assigned responsible client")
		}
		result.Status = Working
	case Submit:
		if state.Status != Working || request.Actor != state.ResponsibleClient || !hasSubmissionEvidence(request.EvidenceKinds) {
			return State{}, fmt.Errorf("submit requires responsible client, diff or artifact, and test evidence")
		}
		result.Status, result.ResponsibleClient = Review, task.Reviewer
	case RequestChanges:
		if state.Status != Review || request.Actor != task.Reviewer || strings.TrimSpace(request.Feedback) == "" {
			return State{}, fmt.Errorf("request_changes requires reviewer and non-empty feedback")
		}
		result.Status, result.ResponsibleClient = RevisionRequired, state.AssignedClient
	case Resume:
		if state.Status != RevisionRequired || request.Actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("resume requires responsible client in REVISION_REQUIRED")
		}
		result.Status = Working
	case Approve:
		if state.Status != Review || request.Actor != task.Reviewer {
			return State{}, fmt.Errorf("approve requires reviewer in REVIEW")
		}
		result.Status = Done
	case Block:
		if (state.Status != Assigned && state.Status != Working && state.Status != Review) ||
			(request.Actor != task.Creator && request.Actor != state.ResponsibleClient) || !hasEvidence(request.EvidenceKinds, EvidenceBlocker) {
			return State{}, fmt.Errorf("block requires allowed state, creator or responsible client, and blocker evidence")
		}
		result.Status = Blocked
	default:
		return State{}, fmt.Errorf("unknown action %q", request.Action)
	}
	result.Version++
	result.LastEventID++
	result.UpdatedAt = request.At
	return result, nil
}

func hasSubmissionEvidence(kinds []EvidenceKind) bool {
	return (hasEvidence(kinds, EvidenceDiff) || hasEvidence(kinds, EvidenceArtifact)) && hasEvidence(kinds, EvidenceTest)
}

func validateEvidenceKinds(kinds []EvidenceKind) error {
	for _, kind := range kinds {
		switch kind {
		case EvidenceDiff, EvidenceArtifact, EvidenceTest, EvidenceBlocker:
		default:
			return fmt.Errorf("unknown evidence kind %q", kind)
		}
	}
	return nil
}

func hasEvidence(kinds []EvidenceKind, wanted EvidenceKind) bool {
	for _, kind := range kinds {
		if kind == wanted {
			return true
		}
	}
	return false
}

func isAllowedResponsible(client string, task Task, assigned string) bool {
	return client == task.Creator || client == task.Reviewer || client == assigned
}

func isBusinessStatus(status Status) bool {
	switch status {
	case Draft, Assigned, Working, Review, RevisionRequired, Done, Blocked:
		return true
	default:
		return false
	}
}
