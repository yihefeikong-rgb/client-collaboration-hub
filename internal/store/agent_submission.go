package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

// ProjectPolicyReader supplies the portable project policy required to decide
// whether a local Agent candidate may advance a task automatically.
type ProjectPolicyReader interface {
	ReadProject(context.Context, string) (protocol.Project, error)
}

// AgentSubmission is a fully parsed local candidate that has not yet mutated
// the task journal. The journal validates it again while holding the task lock.
type AgentSubmission struct {
	ID           string
	Actor        string
	Decision     string
	Action       protocol.Action
	NextAssignee string
	Message      string
	Feedback     string
	Evidence     []protocol.Evidence
	EvidenceRefs []string
	At           time.Time
}

func (s AgentSubmission) Validate() error {
	if !protocol.IsValidID(s.ID) || !protocol.IsValidID(s.Actor) {
		return fmt.Errorf("invalid agent submission identity")
	}
	if err := protocol.ValidateAgentProvenance(s.ID, s.Decision); err != nil {
		return err
	}
	if s.At.IsZero() || s.At.Location() != time.UTC {
		return fmt.Errorf("agent submission time must be UTC and non-zero")
	}
	switch s.Action {
	case protocol.Assign, protocol.Accept, protocol.Submit, protocol.RequestChanges, protocol.Resume, protocol.Approve, protocol.Block, protocol.Message, protocol.AddEvidence:
		return nil
	default:
		return fmt.Errorf("invalid agent submission action %q", s.Action)
	}
}

type AgentSubmissionResult struct {
	State  protocol.State
	Events []protocol.Event
}

type agentPlannedRecord struct {
	Evidence    protocol.Evidence
	HasEvidence bool
	Event       protocol.Event
	Next        protocol.State
}

func (j *FileTaskJournal) CreateTaskFromAgent(ctx context.Context, task protocol.Task, submissionID, actor, decision string, at time.Time) error {
	if task.Creator != actor || !task.CreatedAt.Equal(at) {
		return fmt.Errorf("agent task creation identity or time does not match task")
	}
	if _, err := j.agentProject(ctx, task.ProjectID, decision); err != nil {
		return err
	}
	if err := protocol.ValidateAgentProvenance(submissionID, decision); err != nil {
		return err
	}
	return j.createTask(ctx, task, protocol.Event{
		Origin:         protocol.EventOriginAgent,
		SubmissionID:   submissionID,
		PolicyDecision: decision,
	})
}

func (j *FileTaskJournal) ApplyAgentSubmission(ctx context.Context, taskID string, expectedVersion int64, input AgentSubmission) (AgentSubmissionResult, error) {
	if err := input.Validate(); err != nil {
		return AgentSubmissionResult{}, err
	}
	lock, err := j.Locks.Task(ctx, taskID)
	if err != nil {
		return AgentSubmissionResult{}, err
	}
	defer lock.Unlock()
	report, err := j.inspectUnlocked(taskID)
	if err != nil {
		return AgentSubmissionResult{}, err
	}
	switch report.Health {
	case RecoverableTail:
		return AgentSubmissionResult{}, ErrRecoveryRequired
	case Corrupt:
		return AgentSubmissionResult{}, ErrCorrupt
	}
	if expectedVersion != report.State.Version {
		return AgentSubmissionResult{}, ErrVersionConflict
	}
	task, err := j.readTask(taskID)
	if err != nil {
		return AgentSubmissionResult{}, err
	}
	project, err := j.agentProject(ctx, task.ProjectID, input.Decision)
	if err != nil {
		return AgentSubmissionResult{}, err
	}
	if err := protocol.AuthorizeAgentAction(project.CollaborationPolicy, input.Action); err != nil {
		return AgentSubmissionResult{}, err
	}
	if err := j.policy().Authorize(task, report.State, input.Actor, input.Action, j.References); err != nil {
		return AgentSubmissionResult{}, err
	}
	if err := validateAgentSubmissionPayload(input); err != nil {
		return AgentSubmissionResult{}, err
	}
	planned, final, err := j.planAgentSubmission(ctx, task, report.State, input)
	if err != nil {
		return AgentSubmissionResult{}, err
	}
	for _, record := range planned {
		if !record.HasEvidence {
			continue
		}
		if _, err := j.Evidence.EnsureEvidence(ctx, record.Evidence); err != nil {
			return AgentSubmissionResult{}, err
		}
	}
	current := report.State
	result := AgentSubmissionResult{State: current, Events: make([]protocol.Event, 0, len(planned))}
	for _, record := range planned {
		if err := j.commitRecord(taskID, current, record.Event, record.Next); err != nil {
			return AgentSubmissionResult{}, err
		}
		current = record.Next
		result.State = current
		result.Events = append(result.Events, record.Event)
	}
	if result.State != final {
		return AgentSubmissionResult{}, fmt.Errorf("agent submission result does not match plan")
	}
	return result, nil
}

func (j *FileTaskJournal) agentProject(ctx context.Context, projectID, decision string) (protocol.Project, error) {
	if j.Projects == nil {
		return protocol.Project{}, fmt.Errorf("task journal project policy reader is required")
	}
	project, err := j.Projects.ReadProject(ctx, projectID)
	if err != nil {
		return protocol.Project{}, err
	}
	expected, err := protocol.AgentPolicyDecision(project.CollaborationPolicy)
	if err != nil {
		return protocol.Project{}, err
	}
	if decision != expected {
		return protocol.Project{}, fmt.Errorf("agent submission policy decision does not match project")
	}
	return project, nil
}

func validateAgentSubmissionPayload(input AgentSubmission) error {
	noText := input.Message == "" && input.Feedback == ""
	switch input.Action {
	case protocol.Assign:
		if !protocol.IsValidID(input.NextAssignee) || !noText || len(input.Evidence) != 0 || len(input.EvidenceRefs) != 0 {
			return fmt.Errorf("agent assign submission is invalid")
		}
	case protocol.Accept, protocol.Resume, protocol.Approve:
		if input.NextAssignee != "" || !noText || len(input.Evidence) != 0 || len(input.EvidenceRefs) != 0 {
			return fmt.Errorf("agent %s submission is invalid", input.Action)
		}
	case protocol.Message:
		if strings.TrimSpace(input.Message) == "" || input.NextAssignee != "" || input.Feedback != "" || len(input.Evidence) != 0 || len(input.EvidenceRefs) != 0 {
			return fmt.Errorf("agent message submission is invalid")
		}
	case protocol.AddEvidence:
		if input.NextAssignee != "" || !noText || len(input.Evidence) == 0 || len(input.EvidenceRefs) != 0 {
			return fmt.Errorf("agent evidence submission is invalid")
		}
	case protocol.RequestChanges:
		if input.NextAssignee != "" || input.Message != "" || strings.TrimSpace(input.Feedback) == "" || len(input.Evidence) != 0 || len(input.EvidenceRefs) != 0 {
			return fmt.Errorf("agent request_changes submission is invalid")
		}
	case protocol.Submit, protocol.Block:
		if input.NextAssignee != "" || !noText || len(input.EvidenceRefs) == 0 {
			return fmt.Errorf("agent %s submission is invalid", input.Action)
		}
		if err := requireCandidateEvidenceRefs(input.Evidence, input.EvidenceRefs); err != nil {
			return err
		}
	default:
		return fmt.Errorf("agent submission action is invalid")
	}
	return nil
}

func requireCandidateEvidenceRefs(evidence []protocol.Evidence, refs []string) error {
	seenRefs := map[string]bool{}
	for _, id := range refs {
		if !protocol.IsValidID(id) || seenRefs[id] {
			return fmt.Errorf("agent evidence references are invalid")
		}
		seenRefs[id] = true
	}
	for _, value := range evidence {
		if !seenRefs[value.ID] {
			return fmt.Errorf("agent candidate evidence must be referenced by the action")
		}
	}
	return nil
}

func (j *FileTaskJournal) planAgentSubmission(ctx context.Context, task protocol.Task, initial protocol.State, input AgentSubmission) ([]agentPlannedRecord, protocol.State, error) {
	if j.Evidence == nil {
		return nil, protocol.State{}, fmt.Errorf("task journal evidence store is required")
	}
	announced, err := j.announcedEvidence(task.ID, initial.LastEventID)
	if err != nil {
		return nil, protocol.State{}, err
	}
	kinds := make(map[string]protocol.EvidenceKind, len(announced)+len(input.Evidence))
	for id := range announced {
		value, err := j.Evidence.ReadEvidence(ctx, task.ID, id)
		if err != nil {
			return nil, protocol.State{}, err
		}
		kinds[id] = value.Kind
	}
	planned := make([]agentPlannedRecord, 0, len(input.Evidence)+1)
	state := initial
	seenEvidence := map[string]bool{}
	for _, evidence := range input.Evidence {
		if evidence.TaskID != task.ID || evidence.CreatedBy != input.Actor || evidence.Validate(evidence.ID) != nil || evidence.CreatedAt.Before(state.UpdatedAt) || seenEvidence[evidence.ID] {
			return nil, protocol.State{}, fmt.Errorf("agent evidence submission is invalid")
		}
		seenEvidence[evidence.ID] = true
		if announced[evidence.ID] {
			return nil, protocol.State{}, fmt.Errorf("agent evidence %q is already announced", evidence.ID)
		}
		if _, err := j.Evidence.ReadEvidence(ctx, task.ID, evidence.ID); err == nil {
			return nil, protocol.State{}, fmt.Errorf("%w: evidence %q", ErrEvidenceConflict, evidence.ID)
		} else if !errors.Is(err, ErrNotFound) {
			return nil, protocol.State{}, err
		}
		next := state
		next.Version++
		next.LastEventID++
		next.UpdatedAt = evidence.CreatedAt
		event := protocol.Event{
			EventID:         next.LastEventID,
			TaskID:          task.ID,
			Type:            protocol.EventEvidenceAdded,
			Actor:           input.Actor,
			At:              evidence.CreatedAt,
			Body:            evidence.Summary,
			EvidenceRefs:    []string{evidence.ID},
			ExpectedVersion: state.Version,
			Origin:          protocol.EventOriginAgent,
			SubmissionID:    input.ID,
			PolicyDecision:  input.Decision,
		}
		if err := event.Validate(task.ID); err != nil || next.Validate(task) != nil {
			return nil, protocol.State{}, fmt.Errorf("agent evidence event is invalid")
		}
		planned = append(planned, agentPlannedRecord{Evidence: evidence, HasEvidence: true, Event: event, Next: next})
		kinds[evidence.ID] = evidence.Kind
		state = next
	}
	if input.Action == protocol.AddEvidence {
		return planned, state, nil
	}
	if input.At.Before(state.UpdatedAt) {
		return nil, protocol.State{}, fmt.Errorf("agent submission time is before task state")
	}
	if input.Action == protocol.Assign {
		if err := protocol.ValidateAssignmentTarget(input.NextAssignee, j.References); err != nil {
			return nil, protocol.State{}, err
		}
	}
	if input.Action == protocol.Message {
		next := state
		next.Version++
		next.LastEventID++
		next.UpdatedAt = input.At
		event := protocol.Event{
			EventID:         next.LastEventID,
			TaskID:          task.ID,
			Type:            protocol.EventMessageAdded,
			Actor:           input.Actor,
			At:              input.At,
			Body:            input.Message,
			ExpectedVersion: state.Version,
			Origin:          protocol.EventOriginAgent,
			SubmissionID:    input.ID,
			PolicyDecision:  input.Decision,
		}
		if err := event.Validate(task.ID); err != nil || next.Validate(task) != nil {
			return nil, protocol.State{}, fmt.Errorf("agent message event is invalid")
		}
		return append(planned, agentPlannedRecord{Event: event, Next: next}), next, nil
	}
	evidenceKinds, err := agentEvidenceKinds(input.EvidenceRefs, kinds)
	if err != nil {
		return nil, protocol.State{}, err
	}
	intent := protocol.TransitionIntent{
		Action:         input.Action,
		Actor:          input.Actor,
		NextAssignee:   input.NextAssignee,
		Feedback:       input.Feedback,
		At:             input.At,
		Origin:         protocol.EventOriginAgent,
		SubmissionID:   input.ID,
		PolicyDecision: input.Decision,
	}
	next, err := protocol.Transition(state, task, protocol.TransitionRequest{Action: intent.Action, Actor: intent.Actor, NextAssignee: intent.NextAssignee, Feedback: intent.Feedback, EvidenceKinds: evidenceKinds, At: intent.At})
	if err != nil {
		return nil, protocol.State{}, err
	}
	event, err := eventForTransition(task.ID, state, intent, input.EvidenceRefs)
	if err != nil {
		return nil, protocol.State{}, err
	}
	return append(planned, agentPlannedRecord{Event: event, Next: next}), next, nil
}

func agentEvidenceKinds(refs []string, kinds map[string]protocol.EvidenceKind) ([]protocol.EvidenceKind, error) {
	result := make([]protocol.EvidenceKind, 0, len(refs))
	seen := map[string]bool{}
	for _, id := range refs {
		kind, ok := kinds[id]
		if !ok || seen[id] {
			return nil, fmt.Errorf("agent evidence references are invalid")
		}
		seen[id] = true
		result = append(result, kind)
	}
	return result, nil
}
