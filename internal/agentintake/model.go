package agentintake

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type ReceiptStatus string

const (
	Received ReceiptStatus = "RECEIVED"
	Accepted ReceiptStatus = "ACCEPTED"
	Rejected ReceiptStatus = "REJECTED"
	Unknown  ReceiptStatus = "UNKNOWN"
)

type SubmissionKind string

const (
	ResponseSubmission SubmissionKind = "handoff_response"
	TaskSubmission     SubmissionKind = "task_create"
)

type TaskCreateCandidate struct {
	FormatVersion  string   `json:"format_version"`
	SubmissionID   string   `json:"submission_id"`
	SourceClientID string   `json:"source_client_id"`
	ID             string   `json:"id"`
	ProjectID      string   `json:"project_id"`
	Title          string   `json:"title"`
	Objective      string   `json:"objective"`
	Acceptance     []string `json:"acceptance"`
	Creator        string   `json:"creator"`
	Reviewer       string   `json:"reviewer"`
}

type Receipt struct {
	ID              string          `json:"id"`
	Kind            SubmissionKind  `json:"kind"`
	SourceClientID  string          `json:"source_client_id,omitempty"`
	TaskID          string          `json:"task_id,omitempty"`
	PackageID       string          `json:"package_id,omitempty"`
	Status          ReceiptStatus   `json:"status"`
	Reason          string          `json:"reason,omitempty"`
	ObservedVersion int64           `json:"observed_version,omitempty"`
	CurrentVersion  int64           `json:"current_version,omitempty"`
	ReceivedAt      time.Time       `json:"received_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	AppliedEventIDs []int64         `json:"applied_event_ids"`
	RawSHA256       string          `json:"raw_sha256"`
	Raw             json.RawMessage `json:"raw"`
}

type Result struct {
	Receipt Receipt
	State   protocol.State
}

type ReceiptStore interface {
	LockProcessing(context.Context, string) (store.Lock, error)
	SaveReceived(context.Context, Receipt) (Receipt, bool, error)
	Finalize(context.Context, Receipt) (Receipt, error)
	List(context.Context) ([]Receipt, error)
}
