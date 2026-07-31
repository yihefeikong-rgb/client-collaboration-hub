package agentintake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

const maxCandidateBytes = 1 << 20

var errNoSubmissionEvents = errors.New("agent submission events were not found")

type Service struct {
	Registry         store.RegistryStore
	Journal          store.TaskJournal
	Query            store.TaskQuery
	Receipts         ReceiptStore
	Clock            func() time.Time
	ValidateResponse func(string, []byte) (handoff.ResponseValidation, error)
}

func NewService(registry store.RegistryStore, journal store.TaskJournal, query store.TaskQuery, receipts ReceiptStore, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		Registry:         registry,
		Journal:          journal,
		Query:            query,
		Receipts:         receipts,
		Clock:            clock,
		ValidateResponse: handoff.ValidateResponseBytes,
	}
}

func (s *Service) SubmitResponse(ctx context.Context, packageDir, inputPath string) (Result, error) {
	raw, err := readCandidate(inputPath)
	if err != nil {
		return Result{}, err
	}
	return s.SubmitResponseBytes(ctx, packageDir, raw)
}

func (s *Service) SubmitResponseBytes(ctx context.Context, packageDir string, raw []byte) (Result, error) {
	if len(raw) == 0 || len(raw) > maxCandidateBytes {
		return Result{}, fmt.Errorf("candidate response size is invalid")
	}
	raw = append([]byte(nil), raw...)
	receiptID := ReceiptID(ResponseSubmission, raw)
	lock, err := s.lockProcessing(ctx, receiptID)
	if err != nil {
		return Result{}, err
	}
	defer lock.Unlock()
	receipt, existing, err := s.received(ctx, Receipt{
		ID:         receiptID,
		Kind:       ResponseSubmission,
		Status:     Received,
		RawSHA256:  RawSHA256(raw),
		Raw:        append(json.RawMessage(nil), raw...),
		ReceivedAt: s.now(),
		UpdatedAt:  s.now(),
	})
	if err != nil {
		return Result{}, err
	}
	if existing && receipt.Status != Received {
		return Result{Receipt: receipt}, nil
	}
	if s.ValidateResponse == nil {
		return s.unknown(ctx, receipt, protocol.State{}, errors.New("agent response validator is not configured"))
	}
	validation, err := s.ValidateResponse(packageDir, append([]byte(nil), raw...))
	if err != nil {
		return s.reject(ctx, receipt, protocol.State{}, err)
	}
	receipt.SourceClientID = validation.Response.Actor
	receipt.TaskID = validation.Response.TaskID
	receipt.PackageID = validation.Response.PackageID
	receipt.ObservedVersion = validation.Response.ObservedVersion
	snapshot, err := s.Query.Snapshot(ctx, validation.Response.TaskID, 0)
	if err != nil {
		return s.reject(ctx, receipt, protocol.State{}, err)
	}
	receipt.CurrentVersion = snapshot.State.Version
	if snapshot.Health != store.Healthy {
		return s.reject(ctx, receipt, snapshot.State, fmt.Errorf("task journal health is %s", snapshot.Health))
	}
	if events := submissionEvents(snapshot.Events, receipt.ID); len(events) != 0 {
		return s.reconcileAppliedResponse(ctx, receipt, snapshot.State, validation.Response, events)
	}
	if validation.Response.ObservedVersion != snapshot.State.Version || validation.Response.ObservedThroughEvent != snapshot.State.LastEventID {
		return s.reject(ctx, receipt, snapshot.State, store.ErrVersionConflict)
	}
	project, err := s.Registry.ReadProject(ctx, snapshot.Project.ID)
	if err != nil {
		return s.reject(ctx, receipt, snapshot.State, err)
	}
	decision, err := protocol.AgentPolicyDecision(project.CollaborationPolicy)
	if err != nil {
		return s.reject(ctx, receipt, snapshot.State, err)
	}
	at := s.now()
	if at.Before(snapshot.State.UpdatedAt) {
		at = snapshot.State.UpdatedAt
	}
	evidence := make([]protocol.Evidence, 0, len(validation.Response.Evidence))
	for _, value := range validation.Response.Evidence {
		evidence = append(evidence, protocol.Evidence{
			ID:        value.ID,
			TaskID:    validation.Response.TaskID,
			Kind:      value.Kind,
			Summary:   value.Summary,
			FileRefs:  append([]string(nil), value.FileRefs...),
			CreatedBy: validation.Response.Actor,
			CreatedAt: at,
		})
	}
	applied, err := s.Journal.ApplyAgentSubmission(ctx, validation.Response.TaskID, snapshot.State.Version, store.AgentSubmission{
		ID:           receipt.ID,
		Actor:        validation.Response.Actor,
		Decision:     decision,
		Action:       validation.Response.ProposedAction,
		NextAssignee: validation.Response.NextAssignee,
		Message:      validation.Response.Message,
		Feedback:     validation.Response.Feedback,
		Evidence:     evidence,
		EvidenceRefs: append([]string(nil), validation.Response.EvidenceRefs...),
		At:           at,
	})
	if err != nil {
		if reconciliation, reconciliationErr := s.reconcileResponseAfterJournalError(ctx, receipt, validation.Response); !errors.Is(reconciliationErr, errNoSubmissionEvents) {
			return reconciliation, reconciliationErr
		}
		if errors.Is(err, store.ErrCommitOutcomeUnknown) {
			return s.unknown(ctx, receipt, snapshot.State, err)
		}
		return s.reject(ctx, receipt, snapshot.State, err)
	}
	return s.accept(ctx, receipt, applied.State, applied.Events)
}

func (s *Service) CreateTask(ctx context.Context, inputPath string) (Result, error) {
	raw, err := readCandidate(inputPath)
	if err != nil {
		return Result{}, err
	}
	return s.CreateTaskBytes(ctx, raw)
}

func (s *Service) CreateTaskBytes(ctx context.Context, raw []byte) (Result, error) {
	if len(raw) == 0 || len(raw) > maxCandidateBytes {
		return Result{}, fmt.Errorf("task candidate size is invalid")
	}
	raw = append([]byte(nil), raw...)
	candidate, decodeErr := decodeTaskCreateCandidate(raw)
	receiptID := ReceiptID(TaskSubmission, raw)
	if decodeErr == nil {
		receiptID = candidate.SubmissionID
	}
	lock, err := s.lockProcessing(ctx, receiptID)
	if err != nil {
		return Result{}, err
	}
	defer lock.Unlock()
	receipt, existing, err := s.received(ctx, Receipt{ID: receiptID, Kind: TaskSubmission, Status: Received, RawSHA256: RawSHA256(raw), Raw: append(json.RawMessage(nil), raw...), ReceivedAt: s.now(), UpdatedAt: s.now()})
	if err != nil {
		return Result{}, err
	}
	if existing && receipt.Status != Received {
		return Result{Receipt: receipt}, nil
	}
	if decodeErr != nil {
		return s.reject(ctx, receipt, protocol.State{}, decodeErr)
	}
	receipt.SourceClientID = candidate.SourceClientID
	receipt.TaskID = candidate.ID
	createdAt := s.now()
	task := protocol.Task{ID: candidate.ID, ProjectID: candidate.ProjectID, Title: candidate.Title, Objective: candidate.Objective, Acceptance: append([]string(nil), candidate.Acceptance...), Creator: candidate.Creator, Reviewer: candidate.Reviewer, CreatedAt: createdAt}
	if prior, err := s.Query.Snapshot(ctx, task.ID, 0); err == nil {
		if events := submissionEvents(prior.Events, receipt.ID); len(events) != 0 {
			if len(events) == 1 && events[0].Type == protocol.EventTaskCreated {
				return s.accept(ctx, receipt, prior.State, events)
			}
			return s.unknown(ctx, receipt, prior.State, fmt.Errorf("agent task creation has incomplete journal events"))
		}
	} else if !errors.Is(err, store.ErrTaskNotFound) {
		return s.unknown(ctx, receipt, protocol.State{}, err)
	}
	project, err := s.Registry.ReadProject(ctx, task.ProjectID)
	if err != nil {
		return s.reject(ctx, receipt, protocol.State{}, err)
	}
	decision, err := protocol.AgentPolicyDecision(project.CollaborationPolicy)
	if err != nil {
		return s.reject(ctx, receipt, protocol.State{}, err)
	}
	if candidate.SourceClientID != task.Creator {
		return s.reject(ctx, receipt, protocol.State{}, errors.New("agent task creation source must equal creator"))
	}
	if err := s.Journal.CreateTaskFromAgent(ctx, task, candidate.SubmissionID, candidate.SourceClientID, decision, createdAt); err != nil {
		if reconciliation, reconciliationErr := s.reconcileCreatedTaskAfterJournalError(ctx, receipt, task.ID); !errors.Is(reconciliationErr, errNoSubmissionEvents) {
			return reconciliation, reconciliationErr
		}
		if errors.Is(err, store.ErrCommitOutcomeUnknown) {
			return s.unknown(ctx, receipt, protocol.State{}, err)
		}
		return s.reject(ctx, receipt, protocol.State{}, err)
	}
	state, err := s.Journal.Inspect(ctx, task.ID)
	if err != nil {
		return s.unknown(ctx, receipt, protocol.State{}, err)
	}
	return s.accept(ctx, receipt, state.State, []protocol.Event{{EventID: state.State.LastEventID}})
}

func (s *Service) received(ctx context.Context, initial Receipt) (Receipt, bool, error) {
	if s.Receipts == nil {
		return Receipt{}, false, errors.New("agent receipt store is not configured")
	}
	receipt, created, err := s.Receipts.SaveReceived(ctx, initial)
	return receipt, !created, err
}

func (s *Service) lockProcessing(ctx context.Context, receiptID string) (store.Lock, error) {
	if s.Receipts == nil {
		return nil, errors.New("agent receipt store is not configured")
	}
	return s.Receipts.LockProcessing(ctx, receiptID)
}

func (s *Service) reject(ctx context.Context, receipt Receipt, state protocol.State, cause error) (Result, error) {
	receipt.Status = Rejected
	receipt.Reason = receiptReason(cause)
	receipt.UpdatedAt = s.now()
	if state.Version > 0 {
		receipt.CurrentVersion = state.Version
	}
	final, err := s.Receipts.Finalize(ctx, receipt)
	if err != nil {
		return Result{}, err
	}
	return Result{Receipt: final, State: state}, nil
}

func (s *Service) accept(ctx context.Context, receipt Receipt, state protocol.State, events []protocol.Event) (Result, error) {
	receipt.Status = Accepted
	receipt.Reason = ""
	receipt.UpdatedAt = s.now()
	receipt.CurrentVersion = state.Version
	receipt.AppliedEventIDs = eventIDs(events)
	final, err := s.Receipts.Finalize(ctx, receipt)
	if err != nil {
		return Result{}, err
	}
	return Result{Receipt: final, State: state}, nil
}

func (s *Service) reconcileAppliedResponse(ctx context.Context, receipt Receipt, state protocol.State, response handoff.CandidateResponse, events []protocol.Event) (Result, error) {
	expected := len(response.Evidence)
	if response.ProposedAction != protocol.AddEvidence {
		expected++
	}
	if len(events) == expected {
		return s.accept(ctx, receipt, state, events)
	}
	return s.unknown(ctx, receipt, state, fmt.Errorf("agent submission has %d of %d journal events", len(events), expected))
}

func (s *Service) reconcileResponseAfterJournalError(ctx context.Context, receipt Receipt, response handoff.CandidateResponse) (Result, error) {
	snapshot, err := s.Query.Snapshot(ctx, response.TaskID, 0)
	if err != nil {
		return s.unknown(ctx, receipt, protocol.State{}, fmt.Errorf("read post-commit task state: %w", err))
	}
	if snapshot.Health != store.Healthy {
		return s.unknown(ctx, receipt, snapshot.State, fmt.Errorf("post-commit task journal health is %s", snapshot.Health))
	}
	events := submissionEvents(snapshot.Events, receipt.ID)
	if len(events) == 0 {
		return Result{}, errNoSubmissionEvents
	}
	return s.reconcileAppliedResponse(ctx, receipt, snapshot.State, response, events)
}

func (s *Service) reconcileCreatedTaskAfterJournalError(ctx context.Context, receipt Receipt, taskID string) (Result, error) {
	snapshot, err := s.Query.Snapshot(ctx, taskID, 0)
	if err != nil {
		return s.unknown(ctx, receipt, protocol.State{}, fmt.Errorf("read post-commit task state: %w", err))
	}
	if snapshot.Health != store.Healthy {
		return s.unknown(ctx, receipt, snapshot.State, fmt.Errorf("post-commit task journal health is %s", snapshot.Health))
	}
	events := submissionEvents(snapshot.Events, receipt.ID)
	if len(events) == 0 {
		return Result{}, errNoSubmissionEvents
	}
	if len(events) != 1 || events[0].Type != protocol.EventTaskCreated {
		return s.unknown(ctx, receipt, snapshot.State, errors.New("agent task creation has incomplete journal events"))
	}
	return s.accept(ctx, receipt, snapshot.State, events)
}

func submissionEvents(events []protocol.Event, submissionID string) []protocol.Event {
	matched := make([]protocol.Event, 0)
	for _, event := range events {
		if event.SubmissionID == submissionID {
			matched = append(matched, event)
		}
	}
	return matched
}

func (s *Service) unknown(ctx context.Context, receipt Receipt, state protocol.State, cause error) (Result, error) {
	receipt.Status = Unknown
	receipt.Reason = receiptReason(cause)
	receipt.UpdatedAt = s.now()
	if state.Version > 0 {
		receipt.CurrentVersion = state.Version
	}
	final, finalizeErr := s.Receipts.Finalize(ctx, receipt)
	if finalizeErr != nil {
		return Result{}, finalizeErr
	}
	return Result{Receipt: final, State: state}, cause
}

func (s *Service) now() time.Time {
	return s.Clock().UTC()
}

func receiptReason(cause error) string {
	if cause == nil {
		return "agent submission outcome is unknown"
	}
	if errors.Is(cause, os.ErrNotExist) || errors.Is(cause, os.ErrPermission) {
		return "local candidate or package is unavailable"
	}
	reason := strings.TrimSpace(cause.Error())
	if strings.Contains(reason, `:\`) || strings.Contains(reason, `:/`) || strings.Contains(reason, `/tmp/`) {
		return "candidate validation failed; local path omitted"
	}
	return reason
}

func readCandidate(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxCandidateBytes {
		return nil, fmt.Errorf("candidate input size is invalid")
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("candidate input must be JSON")
	}
	return data, nil
}

func decodeTaskCreateCandidate(data []byte) (TaskCreateCandidate, error) {
	var candidate TaskCreateCandidate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return candidate, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return candidate, fmt.Errorf("task candidate contains multiple JSON values")
	}
	if candidate.FormatVersion != "1" || !protocol.IsValidID(candidate.SubmissionID) || !protocol.IsValidID(candidate.SourceClientID) || !protocol.IsValidID(candidate.ID) || !protocol.IsValidID(candidate.ProjectID) || !protocol.IsValidID(candidate.Creator) || !protocol.IsValidID(candidate.Reviewer) || candidate.Title == "" || candidate.Objective == "" || len(candidate.Acceptance) == 0 {
		return candidate, fmt.Errorf("task candidate is invalid")
	}
	return candidate, nil
}

func eventIDs(events []protocol.Event) []int64 {
	result := make([]int64, 0, len(events))
	for _, event := range events {
		result = append(result, event.EventID)
	}
	return result
}
