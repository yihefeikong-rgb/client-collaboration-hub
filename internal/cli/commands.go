package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

var errUsage = errors.New("invalid command or arguments")

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func (a *App) run(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	switch {
	case len(args) == 1 && args[0] == "init":
		return a.init(jsonOutput)
	case matches(args, "client", "register"):
		return a.clientRegister(ctx, args[2:], jsonOutput)
	case matches(args, "project", "create"):
		return a.projectCreate(ctx, args[2:], jsonOutput)
	case matches(args, "project", "bind"):
		return a.projectBind(ctx, args[2:], jsonOutput)
	case matches(args, "project", "binding-status"):
		return a.projectBindingStatus(ctx, args[2:], jsonOutput)
	case matches(args, "task", "create"):
		return a.taskCreate(ctx, args[2:], jsonOutput)
	case matches(args, "task", "assign"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Assign)
	case matches(args, "task", "accept"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Accept)
	case matches(args, "task", "resume"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Resume)
	case matches(args, "task", "submit"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Submit)
	case matches(args, "task", "block"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Block)
	case matches(args, "message", "add"):
		return a.messageAdd(ctx, args[2:], jsonOutput)
	case matches(args, "evidence", "add"):
		return a.evidenceAdd(ctx, args[2:], jsonOutput)
	case matches(args, "review", "request-changes"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.RequestChanges)
	case matches(args, "review", "approve"):
		return a.transition(ctx, args[2:], jsonOutput, protocol.Approve)
	case matches(args, "status"):
		return a.status(ctx, args[1:], jsonOutput)
	case matches(args, "recover"):
		return a.recover(ctx, args[1:], jsonOutput)
	case matches(args, "handoff", "export"):
		return a.handoffExport(ctx, args[2:], jsonOutput)
	case matches(args, "response", "validate"):
		return a.responseValidate(ctx, args[2:], jsonOutput)
	default:
		return ExitValidation, errUsage
	}
}

func matches(args []string, words ...string) bool {
	if len(args) < len(words) {
		return false
	}
	for index, word := range words {
		if index >= len(args) || args[index] != word {
			return false
		}
	}
	return true
}

func extractJSONFlag(args []string) (bool, []string, error) {
	var result []string
	jsonOutput := false
	for _, arg := range args {
		if arg != "--json" {
			result = append(result, arg)
			continue
		}
		if jsonOutput {
			return false, nil, errUsage
		}
		jsonOutput = true
	}
	return jsonOutput, result, nil
}

func (a *App) init(jsonOutput bool) (int, error) {
	for _, directory := range []string{"projects", "clients", "tasks", "bindings", ".runtime"} {
		if err := os.MkdirAll(filepath.Join(a.Root, "collaboration", directory), 0o700); err != nil {
			return exitCode(err), err
		}
	}
	if err := ensureGitignore(filepath.Join(a.Root, ".gitignore")); err != nil {
		return exitCode(err), err
	}
	if jsonOutput {
		a.writeJSON(map[string]bool{"initialized": true})
	} else {
		fmt.Fprintln(a.Stdout, "initialized: collaboration")
	}
	return ExitOK, nil
}

func ensureGitignore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	contents := string(data)
	missing := make([]string, 0, 3)
	for _, wanted := range []string{"collaboration/.runtime/", "collaboration/bindings/", "collab.exe"} {
		found := false
		for _, line := range strings.Split(contents, "\n") {
			if strings.TrimSpace(line) == wanted {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, wanted)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	appendix := ""
	if len(data) > 0 && !strings.HasSuffix(contents, "\n") {
		appendix += "\n"
	}
	appendix += strings.Join(missing, "\n") + "\n"
	n, writeErr := io.WriteString(file, appendix)
	if writeErr == nil && n != len(appendix) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (a *App) clientRegister(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("client register")
	id, name := fs.String("id", "", ""), fs.String("name", "", "")
	var capabilities stringList
	fs.Var(&capabilities, "capability", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("id", *id, "name", *name); err != nil || len(capabilities) == 0 {
		return ExitValidation, errUsage
	}
	client := protocol.Client{ID: *id, Name: *name, Capabilities: capabilities}
	if err := a.Registry.RegisterClient(ctx, client); err != nil {
		return exitCode(err), err
	}
	if jsonOutput {
		a.writeJSON(map[string]any{"client_id": client.ID, "name": client.Name, "capabilities": client.Capabilities})
	} else {
		fmt.Fprintf(a.Stdout, "client_id: %s\nname: %s\n", client.ID, client.Name)
	}
	return ExitOK, nil
}

func (a *App) projectCreate(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("project create")
	id, name := fs.String("id", "", ""), fs.String("name", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("id", *id, "name", *name); err != nil {
		return ExitValidation, err
	}
	project := protocol.Project{ID: *id, Name: *name, CreatedAt: a.now()}
	if err := a.Registry.CreateProject(ctx, project); err != nil {
		return exitCode(err), err
	}
	if jsonOutput {
		a.writeJSON(map[string]string{"project_id": project.ID, "name": project.Name, "created_at": project.CreatedAt.Format("2006-01-02T15:04:05Z")})
	} else {
		fmt.Fprintf(a.Stdout, "project_id: %s\nname: %s\n", project.ID, project.Name)
	}
	return ExitOK, nil
}

func (a *App) projectBind(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("project bind")
	project := fs.String("project", "", "")
	device := fs.String("device", "", "")
	localPath := fs.String("path", "", "")
	revision := fs.String("revision", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("project", *project, "device", *device, "path", *localPath); err != nil {
		return ExitValidation, errUsage
	}
	binding := store.ProjectBinding{DeviceID: *device, ProjectID: *project, LocalPath: *localPath, Revision: *revision, BoundAt: a.now()}
	if err := a.Bindings.BindProject(ctx, binding); err != nil {
		return exitCode(err), err
	}
	a.writeBinding(jsonOutput, binding.ProjectID, binding.DeviceID, binding.Revision, true)
	return ExitOK, nil
}

func (a *App) projectBindingStatus(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("project binding-status")
	project := fs.String("project", "", "")
	device := fs.String("device", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("project", *project, "device", *device); err != nil {
		return ExitValidation, errUsage
	}
	binding, err := a.Bindings.ReadBinding(ctx, *device, *project)
	if err != nil {
		return exitCode(err), err
	}
	a.writeBinding(jsonOutput, binding.ProjectID, binding.DeviceID, binding.Revision, a.Bindings.BindingAvailable(ctx, binding.DeviceID, binding.ProjectID))
	return ExitOK, nil
}

func (a *App) taskCreate(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("task create")
	id := fs.String("id", "", "")
	project := fs.String("project", "", "")
	title := fs.String("title", "", "")
	objective := fs.String("objective", "", "")
	creator := fs.String("creator", "", "")
	reviewer := fs.String("reviewer", "", "")
	var acceptance stringList
	fs.Var(&acceptance, "acceptance", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("id", *id, "project", *project, "title", *title, "objective", *objective, "creator", *creator); err != nil || len(acceptance) == 0 {
		return ExitValidation, errUsage
	}
	if *reviewer == "" {
		*reviewer = *creator
	}
	task := protocol.Task{ID: *id, ProjectID: *project, Title: *title, Objective: *objective, Acceptance: acceptance, Creator: *creator, Reviewer: *reviewer, CreatedAt: a.now()}
	if err := a.Journal.CreateTask(ctx, task); err != nil {
		return exitCode(err), err
	}
	return a.outputTaskState(ctx, task.ID, jsonOutput)
}

func (a *App) transition(ctx context.Context, args []string, jsonOutput bool, action protocol.Action) (int, error) {
	fs := newFlagSet("transition")
	task := fs.String("task", "", "")
	actor := fs.String("actor", "", "")
	client := fs.String("client", "", "")
	body := fs.String("body", "", "")
	expected := fs.Int64("expected-version", -1, "")
	var evidence stringList
	fs.Var(&evidence, "evidence", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task); err != nil || *expected < 0 {
		return ExitValidation, errUsage
	}
	switch action {
	case protocol.Assign:
		if err := require("client", *client); err != nil {
			return ExitValidation, errUsage
		}
	case protocol.RequestChanges:
		if err := require("actor", *actor, "body", *body); err != nil {
			return ExitValidation, errUsage
		}
	case protocol.Accept, protocol.Resume, protocol.Submit, protocol.Approve, protocol.Block:
		if err := require("actor", *actor); err != nil {
			return ExitValidation, errUsage
		}
	}
	if (action == protocol.Submit || action == protocol.Block) && len(evidence) == 0 {
		return ExitValidation, errUsage
	}
	intent := protocol.TransitionIntent{Action: action, Actor: *actor, NextAssignee: *client, Feedback: *body, At: a.now()}
	state, err := a.Journal.CommitTransition(ctx, *task, *expected, intent, evidence)
	if err != nil {
		return exitCode(err), err
	}
	a.writeState(jsonOutput, state)
	return ExitOK, nil
}

func (a *App) messageAdd(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("message add")
	task, actor, body := fs.String("task", "", ""), fs.String("actor", "", ""), fs.String("body", "", "")
	expected := fs.Int64("expected-version", -1, "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task, "actor", *actor, "body", *body); err != nil || *expected < 0 {
		return ExitValidation, errUsage
	}
	state, err := a.Journal.AppendMessage(ctx, *task, *expected, *actor, *body, a.now())
	if err != nil {
		return exitCode(err), err
	}
	a.writeState(jsonOutput, state)
	return ExitOK, nil
}

func (a *App) evidenceAdd(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("evidence add")
	task, id, kind := fs.String("task", "", ""), fs.String("id", "", ""), fs.String("kind", "", "")
	summary, createdBy := fs.String("summary", "", ""), fs.String("created-by", "", "")
	expected := fs.Int64("expected-version", -1, "")
	var refs stringList
	fs.Var(&refs, "file-ref", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task, "id", *id, "kind", *kind, "summary", *summary, "created-by", *createdBy); err != nil || *expected < 0 {
		return ExitValidation, errUsage
	}
	evidence := protocol.Evidence{ID: *id, TaskID: *task, Kind: protocol.EvidenceKind(*kind), Summary: *summary, FileRefs: refs, CreatedBy: *createdBy, CreatedAt: a.now()}
	result, err := a.Journal.AddEvidence(ctx, *task, *expected, evidence)
	if err != nil {
		return exitCode(err), err
	}
	a.writeEvidenceResult(jsonOutput, result)
	return ExitOK, nil
}

func (a *App) status(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("status")
	task := fs.String("task", "", "")
	device := fs.String("device", "", "")
	actor := fs.String("actor", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task); err != nil {
		return ExitValidation, errUsage
	}
	if *device != "" && !protocol.IsValidID(*device) {
		return ExitValidation, errUsage
	}
	var snapshot store.TaskSnapshot
	var err error
	if *actor == "" {
		snapshot, err = a.Query.Snapshot(ctx, *task, 0)
	} else {
		snapshot, err = a.Query.SnapshotForActor(ctx, *task, 0, *actor)
	}
	if err != nil {
		return exitCode(err), err
	}
	bindingAvailable := false
	if *device != "" && snapshot.Project.ID != "" {
		bindingAvailable = a.Bindings.BindingAvailable(ctx, *device, snapshot.Project.ID)
	}
	a.writeSnapshotHealth(jsonOutput, snapshot, bindingAvailable)
	switch snapshot.Health {
	case store.Healthy:
		return ExitOK, nil
	case store.RecoverableTail:
		return ExitRecovery, nil
	default:
		return ExitCorrupt, nil
	}
}

func (a *App) handoffExport(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("handoff export")
	task := fs.String("task", "", "")
	client := fs.String("client", "", "")
	adapter := fs.String("adapter", "", "")
	device := fs.String("device", "", "")
	afterEvent := fs.Int64("after-event", -1, "")
	output := fs.String("output", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task, "client", *client, "adapter", *adapter, "device", *device, "output", *output); err != nil || *afterEvent < 0 {
		return ExitValidation, errUsage
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(a.Root, outputPath)
	}
	report, err := a.Handoff.Export(ctx, handoff.ExportOptions{TaskID: *task, ClientID: *client, Adapter: *adapter, DeviceID: *device, AfterEventID: *afterEvent, OutputDir: outputPath})
	if err != nil {
		return exitCode(err), err
	}
	a.writeHandoff(jsonOutput, report)
	return ExitOK, nil
}

func (a *App) responseValidate(_ context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("response validate")
	packageDir := fs.String("package", "", "")
	input := fs.String("input", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("package", *packageDir, "input", *input); err != nil {
		return ExitValidation, errUsage
	}
	if !filepath.IsAbs(*packageDir) {
		*packageDir = filepath.Join(a.Root, *packageDir)
	}
	if !filepath.IsAbs(*input) {
		*input = filepath.Join(a.Root, *input)
	}
	result, err := handoff.ValidateResponsePackage(*packageDir, *input)
	if err != nil {
		return exitCode(err), err
	}
	a.writeResponseValidation(jsonOutput, result)
	return ExitOK, nil
}

func (a *App) recover(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("recover")
	task := fs.String("task", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if err := require("task", *task); err != nil {
		return ExitValidation, errUsage
	}
	report, err := a.Journal.RecoverTail(ctx, *task)
	if err != nil {
		if report.Before.Health == store.Corrupt {
			a.writeRecoverCorrupt(jsonOutput, report)
			return exitCode(err), nil
		}
		return exitCode(err), err
	}
	a.writeHealth(jsonOutput, report.After)
	return ExitOK, nil
}

func (a *App) outputTaskState(ctx context.Context, taskID string, jsonOutput bool) (int, error) {
	report, err := a.Journal.Inspect(ctx, taskID)
	if err != nil {
		return exitCode(err), err
	}
	if report.Health != store.Healthy {
		return exitCodeForHealth(report.Health), nil
	}
	a.writeState(jsonOutput, report.State)
	return ExitOK, nil
}

func exitCodeForHealth(health store.Health) int {
	if health == store.RecoverableTail {
		return ExitRecovery
	}
	return ExitCorrupt
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return errUsage
	}
	return nil
}

func require(values ...string) error {
	for index := 0; index < len(values); index += 2 {
		if strings.TrimSpace(values[index+1]) == "" {
			return fmt.Errorf("%s is required", values[index])
		}
	}
	return nil
}
