package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/agentintake"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/webconsole"
)

type appConsoleRunner struct {
	root             string
	workingDirectory string
	clock            func() time.Time
	config           AppConfig
}

func (r appConsoleRunner) RunJSON(ctx context.Context, args []string) webconsole.CommandResult {
	var stdout, stderr bytes.Buffer
	app := NewAppWithConfig(r.root, &stdout, &stderr, r.clock, r.config)
	app.WorkingDirectory = r.workingDirectory
	code, err := app.run(ctx, args, true)
	if err != nil {
		app.writeError(true, err)
	}
	result := webconsole.CommandResult{Code: code, Error: consoleReason(string(bytes.TrimSpace(stderr.Bytes())))}
	if output := bytes.TrimSpace(stdout.Bytes()); len(output) > 0 {
		if json.Valid(output) {
			result.Output = append(json.RawMessage(nil), output...)
		} else if result.Error == "" {
			result.Error = "CLI returned invalid JSON output"
		}
	}
	return result
}

type appConsoleReader struct {
	app *App
}

func (r appConsoleReader) Overview(ctx context.Context, actor, device string) (webconsole.Overview, error) {
	initialized, err := collaborationInitialized(r.app.Root)
	if err != nil {
		return webconsole.Overview{}, err
	}
	registry, ok := r.app.Registry.(store.RegistryCatalog)
	if !ok {
		return webconsole.Overview{}, errors.New("registry does not support console listing")
	}
	clients, err := registry.ListClients(ctx)
	if err != nil {
		return webconsole.Overview{}, err
	}
	projects, err := registry.ListProjects(ctx)
	if err != nil {
		return webconsole.Overview{}, err
	}
	tasks, ok := r.app.Journal.(store.TaskCatalog)
	if !ok {
		return webconsole.Overview{}, errors.New("journal does not support console listing")
	}
	ids, err := tasks.ListTaskIDs(ctx)
	if err != nil {
		return webconsole.Overview{}, err
	}
	result := webconsole.Overview{
		Initialized: initialized,
		Clients:     make([]webconsole.Client, 0, len(clients)),
		Projects:    make([]webconsole.Project, 0, len(projects)),
		Tasks:       make([]webconsole.TaskSummary, 0, len(ids)),
		Submissions: make([]webconsole.AgentSubmission, 0),
		Deliveries:  make([]webconsole.DeliveryView, 0),
	}
	for _, client := range clients {
		result.Clients = append(result.Clients, consoleClient(client))
	}
	for _, project := range projects {
		result.Projects = append(result.Projects, consoleProject(project))
	}
	for _, id := range ids {
		view, err := r.Task(ctx, id, actor, device)
		if err != nil {
			return webconsole.Overview{}, err
		}
		result.Tasks = append(result.Tasks, webconsole.TaskSummary{
			ID:                id,
			ProjectID:         view.Task.ProjectID,
			Title:             view.Task.Title,
			Health:            view.Health,
			Reason:            consoleReason(view.Reason),
			Status:            view.State.Status,
			Version:           view.State.Version,
			LastEventID:       view.State.LastEventID,
			AssignedClient:    view.State.AssignedClient,
			ResponsibleClient: view.State.ResponsibleClient,
		})
	}
	if r.app.Intake != nil && r.app.Intake.Receipts != nil {
		receipts, err := r.app.Intake.Receipts.List(ctx)
		if err != nil {
			return webconsole.Overview{}, err
		}
		for _, receipt := range receipts {
			result.Submissions = append(result.Submissions, consoleSubmission(receipt))
		}
	}
	result.Deliveries = consoleDeliveries(r.app.Root)
	return result, nil
}

func (r appConsoleReader) Task(ctx context.Context, taskID, actor, device string) (webconsole.TaskView, error) {
	if err := requireExistingTaskDirectory(r.app.Root, taskID); err != nil {
		return webconsole.TaskView{}, err
	}
	var snapshot store.TaskSnapshot
	var err error
	if actor == "" {
		snapshot, err = r.app.Query.Snapshot(ctx, taskID, 0)
	} else {
		snapshot, err = r.app.Query.SnapshotForActor(ctx, taskID, 0, actor)
	}
	if err != nil {
		return webconsole.TaskView{}, err
	}
	bindingAvailable := false
	availableBindings := 0
	if snapshot.Project.ID != "" {
		if device != "" {
			bindingAvailable = r.app.Bindings.BindingAvailable(ctx, device, snapshot.Project.ID)
			if bindingAvailable {
				availableBindings = 1
			}
		} else {
			bindings, listErr := r.app.Bindings.ListBindings(ctx, snapshot.Project.ID)
			if listErr != nil {
				return webconsole.TaskView{}, listErr
			}
			for _, binding := range bindings {
				if r.app.Bindings.BindingAvailable(ctx, binding.DeviceID, snapshot.Project.ID) {
					availableBindings++
				}
			}
			bindingAvailable = availableBindings == 1
		}
	}
	view := webconsole.TaskView{
		Health:            string(snapshot.Health),
		Reason:            consoleReason(snapshot.Reason),
		Project:           consoleProject(snapshot.Project),
		Task:              consoleTask(snapshot.Task),
		State:             consoleState(snapshot.State),
		Events:            make([]webconsole.Event, 0, len(snapshot.Events)),
		Evidence:          make([]webconsole.Evidence, 0, len(snapshot.Evidence)),
		ActionActor:       snapshot.ActionActor,
		AllowedActions:    make([]string, 0, len(snapshot.AllowedActions)),
		BindingAvailable:  bindingAvailable,
		AvailableBindings: availableBindings,
		Handoffs:          make([]webconsole.HandoffRecord, 0),
	}
	for _, event := range snapshot.Events {
		view.Events = append(view.Events, consoleEvent(event))
	}
	for _, evidence := range snapshot.Evidence {
		view.Evidence = append(view.Evidence, consoleEvidence(evidence))
	}
	for _, action := range snapshot.AllowedActions {
		view.AllowedActions = append(view.AllowedActions, string(action))
	}
	if r.app.Handoff != nil {
		history, historyErr := r.app.Handoff.ListHistory(ctx, taskID)
		if historyErr != nil {
			return webconsole.TaskView{}, historyErr
		}
		for _, record := range history {
			view.Handoffs = append(view.Handoffs, consoleHandoff(record))
		}
	}
	return view, nil
}

func requireExistingTaskDirectory(root, taskID string) error {
	if !protocol.IsValidID(taskID) {
		return fmt.Errorf("invalid task id %q", taskID)
	}
	info, err := os.Stat(filepath.Join(root, "collaboration", "tasks", taskID))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: task %q", store.ErrTaskNotFound, taskID)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("task %q is not a directory", taskID)
	}
	return nil
}

func (a *App) ui(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	if len(args) != 0 {
		return ExitValidation, errUsage
	}
	server, err := webconsole.NewServer(
		appConsoleRunner{root: a.Root, workingDirectory: a.WorkingDirectory, clock: a.Clock, config: a.Config},
		appConsoleReader{app: a},
	)
	if err != nil {
		return ExitInternal, err
	}
	listener, fallback, err := webconsole.ListenLocal(webconsole.DefaultAddress)
	if err != nil {
		return ExitInternal, err
	}
	defer listener.Close()
	url := "http://" + listener.Addr().String() + "/"
	server.SetOrigin("http://" + listener.Addr().String())
	if jsonOutput {
		a.writeJSON(map[string]any{"url": url, "fallback_port": fallback})
	} else if fallback {
		fmt.Fprintf(a.Stdout, "port 8567 is in use; console is available at: %s\n", url)
	} else {
		fmt.Fprintf(a.Stdout, "console is available at: %s\n", url)
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if err := server.Serve(runCtx, listener); err != nil {
		return ExitInternal, err
	}
	return ExitOK, nil
}

func collaborationInitialized(root string) (bool, error) {
	_, err := os.Stat(filepath.Join(root, "collaboration"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func consoleClient(value protocol.Client) webconsole.Client {
	return webconsole.Client{ID: value.ID, Name: value.Name, Capabilities: append([]string(nil), value.Capabilities...)}
}

func consoleProject(value protocol.Project) webconsole.Project {
	project := webconsole.Project{
		ID:            value.ID,
		Name:          value.Name,
		CreatedAt:     consoleTime(value.CreatedAt),
		FinalReview:   string(value.CollaborationPolicy.FinalReview),
		AutoDone:      value.CollaborationPolicy.AutoDone,
		PolicyVersion: value.PolicyVersion,
	}
	if len(value.PolicyHistory) > 0 {
		latest := value.PolicyHistory[len(value.PolicyHistory)-1]
		project.RecentPolicyAudit = &webconsole.PolicyAuditView{
			Version:  latest.Version,
			Actor:    latest.Actor,
			At:       consoleTime(latest.At),
			Previous: consolePolicyView(latest.Previous),
			Current:  consolePolicyView(latest.Current),
		}
	}
	return project
}

func consolePolicyView(value protocol.CollaborationPolicy) webconsole.PolicyView {
	return webconsole.PolicyView{
		FinalReview: string(value.FinalReview),
		AutoDone:    value.AutoDone,
	}
}

func consoleTask(value protocol.Task) webconsole.Task {
	return webconsole.Task{
		ID:         value.ID,
		ProjectID:  value.ProjectID,
		Title:      value.Title,
		Objective:  value.Objective,
		Acceptance: append([]string(nil), value.Acceptance...),
		Creator:    value.Creator,
		Reviewer:   value.Reviewer,
		CreatedAt:  consoleTime(value.CreatedAt),
	}
}

func consoleState(value protocol.State) webconsole.State {
	return webconsole.State{
		TaskID:            value.TaskID,
		Status:            string(value.Status),
		Version:           value.Version,
		LastEventID:       value.LastEventID,
		AssignedClient:    value.AssignedClient,
		ResponsibleClient: value.ResponsibleClient,
		UpdatedAt:         consoleTime(value.UpdatedAt),
	}
}

func consoleEvent(value protocol.Event) webconsole.Event {
	return webconsole.Event{
		EventID:         value.EventID,
		TaskID:          value.TaskID,
		Type:            string(value.Type),
		Actor:           value.Actor,
		At:              consoleTime(value.At),
		Body:            value.Body,
		EvidenceRefs:    append([]string(nil), value.EvidenceRefs...),
		ExpectedVersion: value.ExpectedVersion,
		TargetClient:    value.TargetClient,
		Origin:          string(value.Origin),
		PolicyDecision:  value.PolicyDecision,
	}
}

func consoleSubmission(value agentintake.Receipt) webconsole.AgentSubmission {
	return webconsole.AgentSubmission{ReceiptID: value.ID, Kind: string(value.Kind), Status: string(value.Status), SourceClientID: value.SourceClientID, TaskID: value.TaskID, PackageID: value.PackageID, Reason: consoleReason(value.Reason), ObservedVersion: value.ObservedVersion, CurrentVersion: value.CurrentVersion, AppliedEventIDs: append([]int64(nil), value.AppliedEventIDs...), ReceivedAt: consoleTime(value.ReceivedAt), UpdatedAt: consoleTime(value.UpdatedAt)}
}

func consoleHandoff(value handoff.HandoffHistoryRecord) webconsole.HandoffRecord {
	return webconsole.HandoffRecord{TargetClient: value.TargetClient, Adapter: value.Adapter, PackageID: value.PackageID, ThroughEvent: value.ThroughEvent, OutputDir: value.OutputDir, CreatedAt: consoleTime(value.CreatedAt), Valid: value.Valid, Reason: consoleReason(value.Reason)}
}

func consoleReason(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, `:\`) || strings.Contains(value, `:/`) || strings.Contains(value, `/tmp/`) {
		return "本机路径已隐藏；请在本机终端查看诊断。"
	}
	return value
}

func consoleEvidence(value protocol.Evidence) webconsole.Evidence {
	return webconsole.Evidence{
		ID:        value.ID,
		TaskID:    value.TaskID,
		Kind:      string(value.Kind),
		Summary:   value.Summary,
		FileRefs:  append([]string(nil), value.FileRefs...),
		CreatedBy: value.CreatedBy,
		CreatedAt: consoleTime(value.CreatedAt),
	}
}

func consoleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

type consoleWakeState struct {
	Notified   map[string]bool                `json:"notified"`
	WakeAt     map[string]time.Time           `json:"wake_at,omitempty"`
	Deliveries map[string]consoleWakeDelivery `json:"deliveries,omitempty"`
}

type consoleWakeDelivery struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Client    string    `json:"client"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// consoleDeliveries exposes the watch-side desktop delivery ledger to the web
// console so a human can see which task was sent to which client and whether
// the desktop conversation confirmed the turn.
func consoleDeliveries(root string) []webconsole.DeliveryView {
	path := filepath.Join(root, "collaboration", ".runtime", "wake-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return []webconsole.DeliveryView{}
	}
	var state consoleWakeState
	if err := json.Unmarshal(data, &state); err != nil {
		return []webconsole.DeliveryView{}
	}
	views := make([]webconsole.DeliveryView, 0, len(state.Deliveries))
	for key, delivery := range state.Deliveries {
		view := webconsole.DeliveryView{
			DeliveryID: delivery.ID,
			TaskID:     delivery.TaskID,
			Client:     delivery.Client,
			Status:     delivery.Status,
			UpdatedAt:  consoleTime(delivery.UpdatedAt),
			Notified:   state.Notified[key],
		}
		if wokenAt, ok := state.WakeAt[key]; ok {
			view.WakeAt = consoleTime(wokenAt)
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].UpdatedAt > views[j].UpdatedAt
	})
	return views
}
