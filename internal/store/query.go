package store

import (
	"context"
	"fmt"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
)

type TaskSnapshot struct {
	Project        protocol.Project
	Task           protocol.Task
	State          protocol.State
	Health         Health
	Reason         string
	Events         []protocol.Event
	Evidence       []protocol.Evidence
	AllowedActions []protocol.Action
	FromEvent      int64
	ThroughEvent   int64
}

type TaskQuery interface {
	Snapshot(context.Context, string, int64) (TaskSnapshot, error)
}

type FileTaskQuery struct {
	Journal  *FileTaskJournal
	Registry RegistryStore
}

func NewFileTaskQuery(journal *FileTaskJournal, registry RegistryStore) *FileTaskQuery {
	return &FileTaskQuery{Journal: journal, Registry: registry}
}

func (q *FileTaskQuery) Snapshot(ctx context.Context, taskID string, afterEventID int64) (TaskSnapshot, error) {
	if q.Journal == nil || q.Registry == nil {
		return TaskSnapshot{}, fmt.Errorf("task query requires journal and registry")
	}
	if afterEventID < 0 {
		return TaskSnapshot{}, fmt.Errorf("after_event must not be negative")
	}
	lock, err := q.Journal.Locks.Task(ctx, taskID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	defer lock.Unlock()
	report, err := q.Journal.inspectUnlocked(taskID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	snapshot := TaskSnapshot{State: report.State, Health: report.Health, Reason: report.Reason, FromEvent: afterEventID, ThroughEvent: report.State.LastEventID}
	if afterEventID > snapshot.ThroughEvent {
		return TaskSnapshot{}, fmt.Errorf("after_event exceeds last_event_id")
	}
	if report.Health != Healthy {
		return snapshot, nil
	}
	task, err := q.Journal.readTask(taskID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	project, err := q.Registry.ReadProject(ctx, task.ProjectID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	events, err := q.Journal.readEvents(taskID)
	if err != nil {
		return TaskSnapshot{}, err
	}
	if int64(len(events)) != snapshot.ThroughEvent {
		return TaskSnapshot{}, fmt.Errorf("event log does not match state")
	}
	snapshot.Project = project
	snapshot.Task = task
	snapshot.Events = append([]protocol.Event(nil), events[afterEventID:]...)
	snapshot.Evidence, err = q.announcedEvidence(ctx, taskID, events)
	if err != nil {
		return TaskSnapshot{}, err
	}
	snapshot.AllowedActions = allowedActions(task, snapshot.State)
	return snapshot, nil
}

func (q *FileTaskQuery) announcedEvidence(ctx context.Context, taskID string, events []protocol.Event) ([]protocol.Evidence, error) {
	evidence := make([]protocol.Evidence, 0)
	for _, event := range events {
		if event.Type != protocol.EventEvidenceAdded {
			continue
		}
		value, err := q.Journal.Evidence.ReadEvidence(ctx, taskID, event.EvidenceRefs[0])
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, value)
	}
	return evidence, nil
}

func allowedActions(task protocol.Task, state protocol.State) []protocol.Action {
	switch state.Status {
	case protocol.Draft:
		if state.ResponsibleClient != task.Creator {
			return nil
		}
		return []protocol.Action{protocol.Assign}
	case protocol.Blocked:
		if state.ResponsibleClient != task.Creator {
			return nil
		}
		return []protocol.Action{protocol.Assign}
	case protocol.Assigned:
		return []protocol.Action{protocol.Accept}
	case protocol.Working:
		return []protocol.Action{protocol.Message, protocol.AddEvidence, protocol.Submit, protocol.Block}
	case protocol.Review:
		return []protocol.Action{protocol.Message, protocol.RequestChanges, protocol.Approve, protocol.Block}
	case protocol.RevisionRequired:
		return []protocol.Action{protocol.Resume}
	default:
		return nil
	}
}
