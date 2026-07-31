package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/agentintake"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

type stateOutput struct {
	TaskID            string          `json:"task_id"`
	Status            protocol.Status `json:"status"`
	Version           int64           `json:"version"`
	LastEventID       int64           `json:"last_event_id"`
	AssignedClient    string          `json:"assigned_client"`
	ResponsibleClient string          `json:"responsible_client"`
	UpdatedAt         string          `json:"updated_at"`
}

type evidenceResultOutput struct {
	stateOutput
	Changed bool `json:"changed"`
}

type healthOutput struct {
	Health string      `json:"health"`
	Reason string      `json:"reason,omitempty"`
	State  stateOutput `json:"state"`
}

type statusOutput struct {
	Health           string            `json:"health"`
	Reason           string            `json:"reason,omitempty"`
	State            stateOutput       `json:"state"`
	ActionActor      string            `json:"action_actor,omitempty"`
	AllowedActions   []protocol.Action `json:"allowed_actions"`
	BindingAvailable bool              `json:"binding_available"`
}

type bindingOutput struct {
	ProjectID string `json:"project_id"`
	DeviceID  string `json:"device_id"`
	Revision  string `json:"revision,omitempty"`
	Available bool   `json:"available"`
}

type recoverErrorOutput struct {
	Error        string `json:"error"`
	Health       string `json:"health"`
	Reason       string `json:"reason,omitempty"`
	BackupPath   string `json:"backup_path,omitempty"`
	BackupStatus string `json:"backup_status"`
}

type handoffOutput struct {
	Adapter      string `json:"adapter"`
	TargetClient string `json:"target_client"`
	TaskID       string `json:"task_id"`
	ProjectID    string `json:"project_id"`
	ThroughEvent int64  `json:"through_event"`
	PackageID    string `json:"package_id"`
}

type handoffNextOutput struct {
	Adapter            string `json:"adapter"`
	TargetClient       string `json:"target_client"`
	TaskID             string `json:"task_id"`
	ProjectID          string `json:"project_id"`
	FromEventExclusive int64  `json:"from_event_exclusive"`
	ThroughEvent       int64  `json:"through_event"`
	PackageID          string `json:"package_id"`
	OutputDir          string `json:"output_dir"`
	Reused             bool   `json:"reused"`
}

type responseValidationOutput struct {
	PackageID      string                `json:"package_id"`
	TaskID         string                `json:"task_id"`
	ActionActor    string                `json:"action_actor"`
	ProposedAction string                `json:"proposed_action"`
	Steps          []handoff.CommandStep `json:"steps"`
}

type agentSubmissionOutput struct {
	ReceiptID       string                     `json:"receipt_id"`
	Kind            agentintake.SubmissionKind `json:"kind"`
	Status          agentintake.ReceiptStatus  `json:"status"`
	SourceClientID  string                     `json:"source_client_id,omitempty"`
	TaskID          string                     `json:"task_id,omitempty"`
	PackageID       string                     `json:"package_id,omitempty"`
	Reason          string                     `json:"reason,omitempty"`
	ObservedVersion int64                      `json:"observed_version,omitempty"`
	CurrentVersion  int64                      `json:"current_version,omitempty"`
	AppliedEventIDs []int64                    `json:"applied_event_ids"`
	State           *stateOutput               `json:"state,omitempty"`
}

func stateResult(state protocol.State) stateOutput {
	return stateOutput{TaskID: state.TaskID, Status: state.Status, Version: state.Version, LastEventID: state.LastEventID, AssignedClient: state.AssignedClient, ResponsibleClient: state.ResponsibleClient, UpdatedAt: state.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func (a *App) writeState(jsonOutput bool, state protocol.State) {
	result := stateResult(state)
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stdout, "task_id: %s\nstatus: %s\nversion: %d\nlast_event_id: %d\nassigned_client: %s\nresponsible_client: %s\nupdated_at: %s\n", result.TaskID, result.Status, result.Version, result.LastEventID, result.AssignedClient, result.ResponsibleClient, result.UpdatedAt)
}

func (a *App) writeEvidenceResult(jsonOutput bool, result store.EvidenceAddResult) {
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(evidenceResultOutput{stateOutput: stateResult(result.State), Changed: result.Changed})
		return
	}
	a.writeState(false, result.State)
	fmt.Fprintf(a.Stdout, "changed: %t\n", result.Changed)
}

func (a *App) writeHealth(jsonOutput bool, report store.HealthReport) {
	result := healthOutput{Health: string(report.Health), Reason: report.Reason, State: stateResult(report.State)}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stdout, "health: %s\n", result.Health)
	if result.Reason != "" {
		fmt.Fprintf(a.Stdout, "reason: %s\n", result.Reason)
	}
	a.writeState(false, report.State)
}

func (a *App) writeSnapshotHealth(jsonOutput bool, snapshot store.TaskSnapshot, bindingAvailable bool) {
	result := statusOutput{
		Health:           string(snapshot.Health),
		Reason:           snapshot.Reason,
		State:            stateResult(snapshot.State),
		ActionActor:      snapshot.ActionActor,
		AllowedActions:   snapshot.AllowedActions,
		BindingAvailable: bindingAvailable,
	}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stdout, "health: %s\n", result.Health)
	if result.Reason != "" {
		fmt.Fprintf(a.Stdout, "reason: %s\n", result.Reason)
	}
	a.writeState(false, snapshot.State)
	fmt.Fprintf(a.Stdout, "action_actor: %s\nallowed_actions: %s\nbinding_available: %t\n", result.ActionActor, strings.Join(actionsToStrings(result.AllowedActions), ","), result.BindingAvailable)
}

func (a *App) writeBinding(jsonOutput bool, projectID, deviceID, revision string, available bool) {
	result := bindingOutput{ProjectID: projectID, DeviceID: deviceID, Revision: revision, Available: available}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stdout, "project_id: %s\ndevice_id: %s\nrevision: %s\navailable: %t\n", result.ProjectID, result.DeviceID, result.Revision, result.Available)
}

func (a *App) writeRecoverCorrupt(jsonOutput bool, report store.RecoveryReport) {
	status := "CREATED"
	if report.BackupError != "" {
		status = "FAILED"
	}
	result := recoverErrorOutput{
		Error:        store.ErrCorrupt.Error(),
		Health:       string(report.Before.Health),
		Reason:       report.Before.Reason,
		BackupPath:   report.BackupPath,
		BackupStatus: status,
	}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stderr, "error: %s\nhealth: %s\n", result.Error, result.Health)
	if result.Reason != "" {
		fmt.Fprintf(a.Stderr, "reason: %s\n", result.Reason)
	}
	if result.BackupPath != "" {
		fmt.Fprintf(a.Stderr, "backup_path: %s\n", result.BackupPath)
	}
	if result.BackupStatus == "FAILED" {
		fmt.Fprintln(a.Stderr, "diagnostic_backup: FAILED")
	}
}

func (a *App) writeHandoff(jsonOutput bool, report handoff.ExportReport) {
	result := handoffOutput{Adapter: report.Adapter, TargetClient: report.TargetClient, TaskID: report.TaskID, ProjectID: report.ProjectID, ThroughEvent: report.ThroughEvent, PackageID: report.PackageID}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(result)
		return
	}
	fmt.Fprintf(a.Stdout, "adapter: %s\ntarget_client: %s\ntask_id: %s\nproject_id: %s\nthrough_event: %d\npackage_id: %s\n", result.Adapter, result.TargetClient, result.TaskID, result.ProjectID, result.ThroughEvent, result.PackageID)
}

func (a *App) writeHandoffNext(jsonOutput bool, report handoff.NextExportReport) {
	output := handoffNextOutput{Adapter: report.Adapter, TargetClient: report.TargetClient, TaskID: report.TaskID, ProjectID: report.ProjectID, FromEventExclusive: report.FromEventExclusive, ThroughEvent: report.ThroughEvent, PackageID: report.PackageID, OutputDir: report.OutputDir, Reused: report.Reused}
	if jsonOutput {
		a.writeJSON(output)
		return
	}
	fmt.Fprintf(a.Stdout, "adapter: %s\ntarget_client: %s\ntask_id: %s\nproject_id: %s\nfrom_event_exclusive: %d\nthrough_event: %d\npackage_id: %s\noutput_dir: %s\nreused: %t\n", output.Adapter, output.TargetClient, output.TaskID, output.ProjectID, output.FromEventExclusive, output.ThroughEvent, output.PackageID, output.OutputDir, output.Reused)
}

func (a *App) writeResponseValidation(jsonOutput bool, result handoff.ResponseValidation) {
	output := responseValidationOutput{PackageID: result.Manifest.PackageID, TaskID: result.Manifest.TaskID, ActionActor: result.Manifest.ActionActor, ProposedAction: string(result.Response.ProposedAction), Steps: result.Steps}
	if jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(output)
		return
	}
	fmt.Fprintf(a.Stdout, "package_id: %s\ntask_id: %s\naction_actor: %s\nproposed_action: %s\n仅供人工审核，不会自动执行：\n", output.PackageID, output.TaskID, output.ActionActor, output.ProposedAction)
	for index, step := range output.Steps {
		fmt.Fprintf(a.Stdout, "%d. %s\n", index+1, formatCommandStep(step))
	}
}

func (a *App) writeAgentSubmission(jsonOutput bool, result agentintake.Result) {
	receipt := result.Receipt
	output := agentSubmissionOutput{
		ReceiptID:       receipt.ID,
		Kind:            receipt.Kind,
		Status:          receipt.Status,
		SourceClientID:  receipt.SourceClientID,
		TaskID:          receipt.TaskID,
		PackageID:       receipt.PackageID,
		Reason:          receipt.Reason,
		ObservedVersion: receipt.ObservedVersion,
		CurrentVersion:  receipt.CurrentVersion,
		AppliedEventIDs: append([]int64(nil), receipt.AppliedEventIDs...),
	}
	if result.State.TaskID != "" {
		state := stateResult(result.State)
		output.State = &state
	}
	if jsonOutput {
		a.writeJSON(output)
		return
	}
	fmt.Fprintf(a.Stdout, "receipt_id: %s\nkind: %s\nstatus: %s\n", output.ReceiptID, output.Kind, output.Status)
	if output.TaskID != "" {
		fmt.Fprintf(a.Stdout, "task_id: %s\n", output.TaskID)
	}
	if output.Reason != "" {
		fmt.Fprintf(a.Stdout, "reason: %s\n", output.Reason)
	}
	if output.State != nil {
		a.writeState(false, result.State)
	}
}

func formatCommandStep(step handoff.CommandStep) string {
	parts := append([]string{step.Program}, step.Args...)
	for index, part := range parts {
		parts[index] = strconv.Quote(part)
	}
	return strings.Join(parts, " ")
}

func actionsToStrings(actions []protocol.Action) []string {
	result := make([]string, len(actions))
	for index, action := range actions {
		result[index] = string(action)
	}
	return result
}

func (a *App) writeJSON(value any) {
	_ = json.NewEncoder(a.Stdout).Encode(value)
}

func (a *App) writeError(jsonOutput bool, err error) {
	message := err.Error()
	if errors.Is(err, handoff.ErrHandoffOutcomeUnknown) {
		message += "; inspect the output directory and do not retry the same path"
	} else if exitCode(err) == ExitUnknown {
		message += "; run collab status before retrying"
	}
	if jsonOutput {
		_ = json.NewEncoder(a.Stderr).Encode(map[string]string{"error": message})
		return
	}
	fmt.Fprintf(a.Stderr, "error: %s\n", message)
}
