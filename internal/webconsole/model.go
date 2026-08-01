package webconsole

import (
	"context"
	"encoding/json"
)

type CommandResult struct {
	Code   int             `json:"code"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type CommandRunner interface {
	RunJSON(context.Context, []string) CommandResult
}

type Reader interface {
	Overview(context.Context, string, string) (Overview, error)
	Task(context.Context, string, string, string) (TaskView, error)
}

type AgentSubmission struct {
	ReceiptID       string  `json:"receipt_id"`
	Kind            string  `json:"kind"`
	Status          string  `json:"status"`
	SourceClientID  string  `json:"source_client_id,omitempty"`
	TaskID          string  `json:"task_id,omitempty"`
	PackageID       string  `json:"package_id,omitempty"`
	Reason          string  `json:"reason,omitempty"`
	ObservedVersion int64   `json:"observed_version,omitempty"`
	CurrentVersion  int64   `json:"current_version,omitempty"`
	AppliedEventIDs []int64 `json:"applied_event_ids"`
	ReceivedAt      string  `json:"received_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type DeliveryView struct {
	DeliveryID string `json:"delivery_id"`
	TaskID     string `json:"task_id"`
	Client     string `json:"client"`
	Status     string `json:"status"`
	UpdatedAt  string `json:"updated_at"`
	Notified   bool   `json:"notified"`
	WakeAt     string `json:"wake_at,omitempty"`
}

type HandoffRecord struct {
	TargetClient string `json:"target_client"`
	Adapter      string `json:"adapter"`
	PackageID    string `json:"package_id"`
	ThroughEvent int64  `json:"through_event"`
	OutputDir    string `json:"output_dir"`
	CreatedAt    string `json:"created_at"`
	Valid        bool   `json:"valid"`
	Reason       string `json:"reason,omitempty"`
}

type Client struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`

	// 项目协作策略数据：终审模式（人工终审或 Agent 终审）、自动完成、策略版本与最近切换审计。
	FinalReview       string           `json:"final_review,omitempty"`
	AutoDone          bool             `json:"auto_done,omitempty"`
	PolicyVersion     int64            `json:"policy_version,omitempty"`
	RecentPolicyAudit *PolicyAuditView `json:"recent_policy_audit,omitempty"`
}

type PolicyAuditView struct {
	Version  int64      `json:"version"`
	Actor    string     `json:"actor"`
	At       string     `json:"at"`
	Previous PolicyView `json:"previous"`
	Current  PolicyView `json:"current"`
}

type PolicyView struct {
	FinalReview string `json:"final_review"`
	AutoDone    bool   `json:"auto_done"`
}

type Task struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"project_id"`
	Title      string   `json:"title"`
	Objective  string   `json:"objective"`
	Acceptance []string `json:"acceptance"`
	Creator    string   `json:"creator"`
	Reviewer   string   `json:"reviewer"`
	CreatedAt  string   `json:"created_at"`
}

type State struct {
	TaskID            string `json:"task_id"`
	Status            string `json:"status"`
	Version           int64  `json:"version"`
	LastEventID       int64  `json:"last_event_id"`
	AssignedClient    string `json:"assigned_client"`
	ResponsibleClient string `json:"responsible_client"`
	UpdatedAt         string `json:"updated_at"`
}

type Event struct {
	EventID         int64    `json:"event_id"`
	TaskID          string   `json:"task_id"`
	Type            string   `json:"type"`
	Actor           string   `json:"actor"`
	At              string   `json:"at"`
	Body            string   `json:"body"`
	EvidenceRefs    []string `json:"evidence_refs"`
	ExpectedVersion int64    `json:"expected_version"`
	TargetClient    string   `json:"target_client,omitempty"`
	Origin          string   `json:"origin,omitempty"`
	PolicyDecision  string   `json:"policy_decision,omitempty"`
}

type Evidence struct {
	ID        string   `json:"id"`
	TaskID    string   `json:"task_id"`
	Kind      string   `json:"kind"`
	Summary   string   `json:"summary"`
	FileRefs  []string `json:"file_refs"`
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
}

type TaskSummary struct {
	ID                string `json:"id"`
	ProjectID         string `json:"project_id,omitempty"`
	Title             string `json:"title,omitempty"`
	Health            string `json:"health"`
	Reason            string `json:"reason,omitempty"`
	Status            string `json:"status,omitempty"`
	Version           int64  `json:"version"`
	LastEventID       int64  `json:"last_event_id"`
	AssignedClient    string `json:"assigned_client,omitempty"`
	ResponsibleClient string `json:"responsible_client,omitempty"`
}

type Overview struct {
	Initialized bool              `json:"initialized"`
	Clients     []Client          `json:"clients"`
	Projects    []Project         `json:"projects"`
	Tasks       []TaskSummary     `json:"tasks"`
	Submissions []AgentSubmission `json:"submissions"`
	Deliveries  []DeliveryView    `json:"deliveries"`
}

type TaskView struct {
	Health            string          `json:"health"`
	Reason            string          `json:"reason,omitempty"`
	Project           Project         `json:"project"`
	Task              Task            `json:"task"`
	State             State           `json:"state"`
	Events            []Event         `json:"events"`
	Evidence          []Evidence      `json:"evidence"`
	ActionActor       string          `json:"action_actor"`
	AllowedActions    []string        `json:"allowed_actions"`
	BindingAvailable  bool            `json:"binding_available"`
	AvailableBindings int             `json:"available_bindings"`
	Handoffs          []HandoffRecord `json:"handoffs"`
}
