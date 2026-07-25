package protocol

import "fmt"

// ActionPolicy is the single source of actor, role, capability, and status
// authorization for Journal, Replay, queries, and handoff generation.
type ActionPolicy interface {
	AllowedActions(Task, State, string, References) []Action
	Authorize(Task, State, string, Action, References) error
}

type DefaultActionPolicy struct{}

func (DefaultActionPolicy) AllowedActions(task Task, state State, actor string, refs References) []Action {
	actions := make([]Action, 0, len(allActions))
	for _, action := range allActions {
		if (DefaultActionPolicy{}).Authorize(task, state, actor, action, refs) == nil {
			actions = append(actions, action)
		}
	}
	return actions
}

func (DefaultActionPolicy) Authorize(task Task, state State, actor string, action Action, refs References) error {
	if refs == nil {
		return fmt.Errorf("action policy requires references")
	}
	if err := state.Validate(task); err != nil {
		return err
	}
	if !IsValidID(actor) || !refs.ClientExists(actor) {
		return fmt.Errorf("action actor %q is not registered", actor)
	}
	if state.Status == Done {
		return fmt.Errorf("DONE does not permit %s", action)
	}
	switch action {
	case Assign:
		if state.Status != Draft && state.Status != Blocked {
			return fmt.Errorf("assign is not allowed in %s", state.Status)
		}
		return requireActorCapability(actor, task.Creator, "create_task", refs)
	case Accept:
		if state.Status != Assigned {
			return fmt.Errorf("accept is not allowed in %s", state.Status)
		}
		return requireActorCapability(actor, state.AssignedClient, "execute", refs)
	case Submit:
		if state.Status != Working {
			return fmt.Errorf("submit is not allowed in %s", state.Status)
		}
		return requireActorCapability(actor, state.AssignedClient, "execute", refs)
	case RequestChanges, Approve:
		if state.Status != Review {
			return fmt.Errorf("%s is not allowed in %s", action, state.Status)
		}
		return requireActorCapability(actor, task.Reviewer, "review", refs)
	case Resume:
		if state.Status != RevisionRequired {
			return fmt.Errorf("resume is not allowed in %s", state.Status)
		}
		return requireActorCapability(actor, state.AssignedClient, "execute", refs)
	case Block:
		if state.Status != Assigned && state.Status != Working && state.Status != Review {
			return fmt.Errorf("block is not allowed in %s", state.Status)
		}
		if actor == task.Creator {
			return requireActorCapability(actor, task.Creator, "create_task", refs)
		}
		if actor != state.ResponsibleClient {
			return fmt.Errorf("block actor %q is not responsible", actor)
		}
		if actor == task.Reviewer {
			return requireActorCapability(actor, task.Reviewer, "review", refs)
		}
		return requireActorCapability(actor, state.AssignedClient, "execute", refs)
	case Message, AddEvidence:
		if !isTaskParticipant(actor, task, state) {
			return fmt.Errorf("%s actor %q is not a task participant", action, actor)
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

var allActions = []Action{Assign, Accept, Submit, RequestChanges, Resume, Approve, Block, Message, AddEvidence}

func requireActorCapability(actor, expected, capability string, refs References) error {
	if actor != expected {
		return fmt.Errorf("action actor %q is not permitted", actor)
	}
	if !refs.ClientHasCapability(actor, capability) {
		return fmt.Errorf("actor %q lacks %s capability", actor, capability)
	}
	return nil
}

func isTaskParticipant(actor string, task Task, state State) bool {
	return actor == task.Creator || actor == task.Reviewer || (state.AssignedClient != "" && actor == state.AssignedClient)
}
