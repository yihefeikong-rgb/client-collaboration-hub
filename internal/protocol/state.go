package protocol

import "fmt"

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

type State struct {
	TaskID            string `json:"task_id"`
	Status            Status `json:"status"`
	Version           int64  `json:"version"`
	LastEventID       int64  `json:"last_event_id"`
	ResponsibleClient string `json:"responsible_client"`
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

func (s State) Validate() error {
	if err := validateID("state task_id", s.TaskID, ""); err != nil {
		return err
	}
	if !isBusinessStatus(s.Status) {
		return fmt.Errorf("invalid business status %q", s.Status)
	}
	if s.Version < 0 || s.LastEventID < 0 {
		return fmt.Errorf("state version and last_event_id must not be negative")
	}
	if s.Status != Draft {
		if err := validateID("responsible_client", s.ResponsibleClient, ""); err != nil {
			return err
		}
	}
	return nil
}

func Transition(state State, task Task, actor string, action Action, nextAssignee string) (State, error) {
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	if state.TaskID != task.ID {
		return State{}, fmt.Errorf("state task_id does not match task")
	}
	result := state
	switch action {
	case Assign:
		if actor != task.Creator || (state.Status != Draft && state.Status != Blocked) || nextAssignee == "" {
			return State{}, fmt.Errorf("assign requires creator, DRAFT or BLOCKED, and an assignee")
		}
		result.Status, result.ResponsibleClient = Assigned, nextAssignee
	case Accept:
		if state.Status != Assigned || actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("accept requires assigned responsible client")
		}
		result.Status = Working
	case Submit:
		if state.Status != Working || actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("submit requires working responsible client")
		}
		result.Status = Review
	case RequestChanges:
		if state.Status != Review || actor != task.Reviewer {
			return State{}, fmt.Errorf("request_changes requires reviewer in REVIEW")
		}
		result.Status = RevisionRequired
	case Resume:
		if state.Status != RevisionRequired || actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("resume requires responsible client in REVISION_REQUIRED")
		}
		result.Status = Working
	case Approve:
		if state.Status != Review || actor != task.Reviewer {
			return State{}, fmt.Errorf("approve requires reviewer in REVIEW")
		}
		result.Status = Done
	case Block:
		if state.Status != Assigned && state.Status != Working && state.Status != Review {
			return State{}, fmt.Errorf("block requires ASSIGNED, WORKING, or REVIEW")
		}
		if actor != task.Creator && actor != state.ResponsibleClient {
			return State{}, fmt.Errorf("block requires creator or responsible client")
		}
		result.Status = Blocked
	default:
		return State{}, fmt.Errorf("unknown action %q", action)
	}
	result.Version++
	result.LastEventID++
	return result, nil
}

func isBusinessStatus(status Status) bool {
	switch status {
	case Draft, Assigned, Working, Review, RevisionRequired, Done, Blocked:
		return true
	default:
		return false
	}
}
