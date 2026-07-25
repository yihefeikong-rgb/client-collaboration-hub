package cli

import (
	"encoding/json"
	"fmt"

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

type healthOutput struct {
	Health string      `json:"health"`
	Reason string      `json:"reason,omitempty"`
	State  stateOutput `json:"state"`
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

func (a *App) writeJSON(value any) {
	_ = json.NewEncoder(a.Stdout).Encode(value)
}

func (a *App) writeError(jsonOutput bool, err error) {
	message := err.Error()
	if exitCode(err) == ExitUnknown {
		message += "; run collab status before retrying"
	}
	if jsonOutput {
		_ = json.NewEncoder(a.Stderr).Encode(map[string]string{"error": message})
		return
	}
	fmt.Fprintf(a.Stderr, "error: %s\n", message)
}
