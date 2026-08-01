package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpguide "github.com/yihefeikong-rgb/client-collaboration-hub/docs/mcp"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/agentintake"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/handoff"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/version"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/webconsole"
)

const mcpGuideURI = "collab://manual/agent-operating-guide"

type emptyInput struct{}

type projectsOutput struct {
	Projects []webconsole.Project `json:"projects"`
}

type registerProjectInput struct {
	Path string `json:"path" jsonschema:"absolute local path of the existing source repository"`
	ID   string `json:"id,omitempty" jsonschema:"optional stable project id; omit to derive it"`
	Name string `json:"name,omitempty" jsonschema:"optional display name; omit to use the directory name"`
}

type tasksInput struct {
	ProjectID string `json:"project_id,omitempty" jsonschema:"optional project id filter"`
	Status    string `json:"status,omitempty" jsonschema:"optional exact task status filter"`
}

type tasksOutput struct {
	Tasks []webconsole.TaskSummary `json:"tasks"`
}

type taskInput struct {
	TaskID string `json:"task_id" jsonschema:"task id to inspect"`
}

type taskOutput struct {
	Task webconsole.TaskView `json:"task"`
}

type nextWorkInput struct {
	ClientID string `json:"client_id" jsonschema:"client whose current responsibilities should be returned"`
}

type createTaskInput struct {
	ID             string   `json:"id,omitempty" jsonschema:"optional task id; omit to generate one"`
	ProjectID      string   `json:"project_id" jsonschema:"registered logical project id"`
	Title          string   `json:"title" jsonschema:"short task title"`
	Objective      string   `json:"objective" jsonschema:"specific outcome the task must achieve"`
	Acceptance     []string `json:"acceptance" jsonschema:"one or more verifiable acceptance criteria"`
	SourceClientID string   `json:"source_client_id" jsonschema:"registered client creating the task"`
	Reviewer       string   `json:"reviewer,omitempty" jsonschema:"final reviewer client; defaults to codex"`
}

type submissionOutput struct {
	Receipt mcpReceipt     `json:"receipt"`
	State   protocol.State `json:"state"`
}

// mcpReceipt mirrors agentintake.Receipt for MCP tool output. Raw is declared
// as any so the inferred JSON schema matches the actual serialized value: the
// raw candidate is arbitrary JSON (typically an object), while a
// json.RawMessage would be inferred as an array and fail output validation.
type mcpReceipt struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	SourceClientID  string    `json:"source_client_id,omitempty"`
	TaskID          string    `json:"task_id,omitempty"`
	PackageID       string    `json:"package_id,omitempty"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	ObservedVersion int64     `json:"observed_version,omitempty"`
	CurrentVersion  int64     `json:"current_version,omitempty"`
	ReceivedAt      time.Time `json:"received_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	AppliedEventIDs []int64   `json:"applied_event_ids"`
	RawSHA256       string    `json:"raw_sha256"`
	Raw             any       `json:"raw"`
}

func toMCPReceipt(r agentintake.Receipt) mcpReceipt {
	var raw any
	if len(r.Raw) > 0 {
		_ = json.Unmarshal(r.Raw, &raw)
	}
	return mcpReceipt{
		ID:              r.ID,
		Kind:            string(r.Kind),
		SourceClientID:  r.SourceClientID,
		TaskID:          r.TaskID,
		PackageID:       r.PackageID,
		Status:          string(r.Status),
		Reason:          r.Reason,
		ObservedVersion: r.ObservedVersion,
		CurrentVersion:  r.CurrentVersion,
		ReceivedAt:      r.ReceivedAt,
		UpdatedAt:       r.UpdatedAt,
		AppliedEventIDs: r.AppliedEventIDs,
		RawSHA256:       r.RawSHA256,
		Raw:             raw,
	}
}

type mcpHandoffOutput struct {
	Report handoff.NextExportReport `json:"report"`
	Files  map[string]string        `json:"files"`
}

type submitCandidateInput struct {
	PackageDir string                    `json:"package_dir" jsonschema:"handoff output_dir returned by collab_generate_handoff"`
	Candidate  handoff.CandidateResponse `json:"candidate" jsonschema:"completed candidate response matching the package template"`
}

type eventsOutput struct {
	Events []webconsole.Event `json:"events"`
}

type evidenceOutput struct {
	Evidence []webconsole.Evidence `json:"evidence"`
}

type submissionsOutput struct {
	Submissions []webconsole.AgentSubmission `json:"submissions"`
}

type claimInput struct {
	TaskID   string `json:"task_id" jsonschema:"task to claim or release"`
	Actor    string `json:"actor" jsonschema:"claiming client id"`
	Worktree string `json:"worktree" jsonschema:"absolute path of the isolated working tree; required unless release is true"`
	Release  bool   `json:"release,omitempty" jsonschema:"release the claim instead of (re)claiming"`
}

type claimOutput struct {
	TaskID    string `json:"task_id"`
	ClaimedBy string `json:"claimed_by"`
	Worktree  string `json:"worktree"`
	ClaimedAt string `json:"claimed_at,omitempty"`
	Released  bool   `json:"released,omitempty"`
}

func (a *App) NewMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:        "client-collaboration-hub",
		Title:       "Client Collaboration Hub",
		Version:     version.Version,
		Description: "Local auditable collaboration hub for independent AI clients with human final review.",
	}, &mcp.ServerOptions{
		Instructions: "Read collab://manual/agent-operating-guide before writing. Agents may create and submit collaboration facts, but final approve and request-changes are human-only web actions.",
		Capabilities: &mcp.ServerCapabilities{},
	})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false)}
	additive := &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(false)}
	nonIdempotentAdditive := &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: false, OpenWorldHint: boolPointer(false)}

	mcp.AddTool(server, &mcp.Tool{Name: "collab_list_projects", Title: "列出协作项目", Description: "List all projects registered in the global local hub.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, projectsOutput, error) {
			overview, err := appConsoleReader{app: a}.Overview(ctx, "", DefaultDeviceID())
			return nil, projectsOutput{Projects: overview.Projects}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_register_project", Title: "登记本机项目", Description: "Register and bind one existing local source directory. Repeating the same directory is idempotent.", Annotations: additive},
		func(ctx context.Context, _ *mcp.CallToolRequest, input registerProjectInput) (*mcp.CallToolResult, LocalProjectResult, error) {
			if !filepath.IsAbs(input.Path) {
				return nil, LocalProjectResult{}, errors.New("project path must be absolute")
			}
			result, err := a.RegisterLocalProject(ctx, input.ID, input.Name, input.Path)
			return nil, result, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_list_tasks", Title: "列出任务", Description: "List verified task summaries, optionally filtered by project or exact status.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input tasksInput) (*mcp.CallToolResult, tasksOutput, error) {
			overview, err := appConsoleReader{app: a}.Overview(ctx, "", DefaultDeviceID())
			if err != nil {
				return nil, tasksOutput{}, err
			}
			tasks := make([]webconsole.TaskSummary, 0, len(overview.Tasks))
			for _, task := range overview.Tasks {
				if input.ProjectID != "" && task.ProjectID != input.ProjectID {
					continue
				}
				if input.Status != "" && task.Status != input.Status {
					continue
				}
				tasks = append(tasks, task)
			}
			return nil, tasksOutput{Tasks: tasks}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_get_task", Title: "读取任务", Description: "Read a verified task snapshot, events, Evidence, allowed actions, Binding health, and handoff history.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskInput) (*mcp.CallToolResult, taskOutput, error) {
			view, err := appConsoleReader{app: a}.Task(ctx, input.TaskID, "", DefaultDeviceID())
			return nil, taskOutput{Task: view}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_get_next_work", Title: "读取客户端下一步工作", Description: "List unfinished tasks currently assigned or responsible to one client.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input nextWorkInput) (*mcp.CallToolResult, tasksOutput, error) {
			if !protocol.IsValidID(input.ClientID) {
				return nil, tasksOutput{}, errors.New("client_id is invalid")
			}
			overview, err := appConsoleReader{app: a}.Overview(ctx, input.ClientID, DefaultDeviceID())
			if err != nil {
				return nil, tasksOutput{}, err
			}
			tasks := make([]webconsole.TaskSummary, 0)
			for _, task := range overview.Tasks {
				if task.Status != string(protocol.Done) && (task.ResponsibleClient == input.ClientID || task.AssignedClient == input.ClientID) {
					tasks = append(tasks, task)
				}
			}
			return nil, tasksOutput{Tasks: tasks}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_task_claim", Title: "认领任务工作区", Description: "Claim or release an isolated worktree for a task. Only the assigned or responsible client can claim, and a task can have exactly one claimer at a time; this prevents parallel agents from editing the same directory.", Annotations: additive},
		func(ctx context.Context, _ *mcp.CallToolRequest, input claimInput) (*mcp.CallToolResult, claimOutput, error) {
			if !protocol.IsValidID(input.TaskID) || !protocol.IsValidID(input.Actor) {
				return nil, claimOutput{}, errors.New("task_id or actor is invalid")
			}
			if input.Release {
				if _, err := a.claimTask(ctx, input.TaskID, input.Actor, "", true); err != nil {
					return nil, claimOutput{}, err
				}
				return nil, claimOutput{TaskID: input.TaskID, ClaimedBy: "", Worktree: "", Released: true}, nil
			}
			record, err := a.claimTask(ctx, input.TaskID, input.Actor, input.Worktree, false)
			if err != nil {
				return nil, claimOutput{}, err
			}
			return nil, claimOutput{TaskID: record.TaskID, ClaimedBy: record.ClaimedBy, Worktree: record.Worktree, ClaimedAt: record.ClaimedAt.UTC().Format(time.RFC3339Nano)}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_create_task", Title: "创建任务候选", Description: "Create an auditable task through agent intake. The task starts in DRAFT and cannot bypass policy. Supply a stable task id when retry safety is required.", Annotations: nonIdempotentAdditive},
		func(ctx context.Context, _ *mcp.CallToolRequest, input createTaskInput) (*mcp.CallToolResult, submissionOutput, error) {
			taskID := input.ID
			if taskID == "" {
				var err error
				taskID, err = generatedID("task")
				if err != nil {
					return nil, submissionOutput{}, err
				}
			}
			reviewer := input.Reviewer
			if reviewer == "" {
				reviewer = "codex"
			}
			submissionID, err := generatedID("mcp")
			if err != nil {
				return nil, submissionOutput{}, err
			}
			candidate := agentintake.TaskCreateCandidate{
				FormatVersion: "1", SubmissionID: submissionID, SourceClientID: input.SourceClientID,
				ID: taskID, ProjectID: input.ProjectID, Title: input.Title, Objective: input.Objective,
				Acceptance: append([]string(nil), input.Acceptance...), Creator: input.SourceClientID, Reviewer: reviewer,
			}
			raw, err := json.Marshal(candidate)
			if err != nil {
				return nil, submissionOutput{}, err
			}
			result, err := a.Intake.CreateTaskBytes(ctx, raw)
			return nil, submissionOutput{Receipt: toMCPReceipt(result.Receipt), State: result.State}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_generate_handoff", Title: "生成下一份交接包", Description: "Generate or reuse the next verified handoff package using the current target, Binding, and event cursor.", Annotations: additive},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskInput) (*mcp.CallToolResult, mcpHandoffOutput, error) {
			report, err := a.Handoff.ExportNext(ctx, input.TaskID)
			if err != nil {
				return nil, mcpHandoffOutput{}, err
			}
			files, err := a.readHandoffFiles(report.OutputDir)
			return nil, mcpHandoffOutput{Report: report, Files: files}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_submit_candidate", Title: "提交候选响应", Description: "Submit a completed handoff candidate through captured-byte validation, receipt locking, policy checks, and Journal reconciliation.", Annotations: additive},
		func(ctx context.Context, _ *mcp.CallToolRequest, input submitCandidateInput) (*mcp.CallToolResult, submissionOutput, error) {
			raw, err := json.Marshal(input.Candidate)
			if err != nil {
				return nil, submissionOutput{}, err
			}
			packageDir, err := a.managedHandoffDirectory(ctx, input.PackageDir)
			if err != nil {
				return nil, submissionOutput{}, err
			}
			result, err := a.Intake.SubmitResponseBytes(ctx, packageDir, raw)
			return nil, submissionOutput{Receipt: toMCPReceipt(result.Receipt), State: result.State}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_list_events", Title: "读取任务事件", Description: "List the append-only verified event ledger for one task.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskInput) (*mcp.CallToolResult, eventsOutput, error) {
			view, err := appConsoleReader{app: a}.Task(ctx, input.TaskID, "", DefaultDeviceID())
			return nil, eventsOutput{Events: view.Events}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_list_evidence", Title: "读取任务 Evidence", Description: "List verified Evidence registered for one task.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input taskInput) (*mcp.CallToolResult, evidenceOutput, error) {
			view, err := appConsoleReader{app: a}.Task(ctx, input.TaskID, "", DefaultDeviceID())
			return nil, evidenceOutput{Evidence: view.Evidence}, err
		})

	mcp.AddTool(server, &mcp.Tool{Name: "collab_list_submissions", Title: "读取 Agent 提交回执", Description: "List persisted Agent intake receipts, including rejected and unknown outcomes.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, submissionsOutput, error) {
			overview, err := appConsoleReader{app: a}.Overview(ctx, "", DefaultDeviceID())
			return nil, submissionsOutput{Submissions: overview.Submissions}, err
		})

	server.AddResource(&mcp.Resource{
		URI: mcpGuideURI, Name: "agent-operating-guide", Title: "Agent 操作说明",
		Description: "Required operating rules and workflow for AI clients using this collaboration hub.",
		MIMEType:    "text/markdown; charset=utf-8", Size: int64(len(mcpguide.OperatingGuide)),
	}, func(_ context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := ""
		if request != nil && request.Params != nil {
			uri = request.Params.URI
		}
		if uri != mcpGuideURI {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: mcpGuideURI, MIMEType: "text/markdown; charset=utf-8", Text: mcpguide.OperatingGuide,
		}}}, nil
	})
	return server
}

func (a *App) managedHandoffDirectory(ctx context.Context, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("package_dir is required")
	}
	directory := filepath.FromSlash(value)
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(a.Root, directory)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", errors.New("handoff package is unavailable")
	}
	handoffRoot, err := filepath.EvalSymlinks(filepath.Join(a.Root, "collaboration", ".runtime", "handoffs"))
	if err != nil {
		return "", errors.New("managed handoff directory is unavailable")
	}
	relative, err := filepath.Rel(handoffRoot, resolved)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("handoff package is outside the managed handoff directory")
	}
	manifest, err := handoff.VerifyPackage(resolved)
	if err != nil {
		return "", fmt.Errorf("handoff package verification failed: %w", err)
	}
	records, err := a.Handoff.ListHistory(ctx, manifest.TaskID)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if !record.Valid || record.PackageID != manifest.PackageID || record.TargetClient != manifest.TargetData.ID ||
			record.Adapter != manifest.Adapter || record.ThroughEvent != manifest.ThroughEvent {
			continue
		}
		recordPath, pathErr := filepath.EvalSymlinks(filepath.Join(a.Root, filepath.FromSlash(record.OutputDir)))
		if pathErr == nil && sameLocalPath(recordPath, resolved) {
			return filepath.Clean(resolved), nil
		}
	}
	return "", errors.New("handoff package is not present in verified handoff history")
}

func (a *App) runMCP(ctx context.Context) error {
	return a.NewMCPServer().Run(ctx, &CompatibleStdioTransport{})
}

func (a *App) readHandoffFiles(outputDir string) (map[string]string, error) {
	clean := filepath.Clean(filepath.FromSlash(outputDir))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, errors.New("handoff output path is invalid")
	}
	directory := filepath.Join(a.Root, clean)
	handoffRoot := filepath.Join(a.Root, "collaboration", ".runtime", "handoffs")
	relative, err := filepath.Rel(handoffRoot, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("handoff output is outside the managed handoff directory")
	}
	files := make(map[string]string, 4)
	for _, name := range []string{"handoff.md", "manifest.json", "candidate-response.schema.json", "candidate-response.json"} {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		files[name] = string(data)
	}
	return files, nil
}

func generatedID(prefix string) (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate submission id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(random), nil
}

func boolPointer(value bool) *bool {
	return &value
}
