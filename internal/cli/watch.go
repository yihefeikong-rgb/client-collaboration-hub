package cli

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/protocol"
	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/store"
)

const (
	defaultWatchInterval  = 3 * time.Second
	wakeStateFileName     = "wake-state.json"
	wakeStateLockFileName = "watch-state.lock"
	ccSessionsFileName    = "cc-sessions.json"
)

const (
	ccExecutionPrompt     = `协作中枢给你分配了任务。请读取 collab://manual/agent-operating-guide 资源，然后调用 collab_get_next_work（client_id 用 cc-haha）找到你的任务，再调用 collab_get_task 读取任务详情和验收标准。若任务尚未认领工作区，先调用 collab_task_claim 认领一个独立工作区（真实存在的绝对目录），并只在该目录内实施修改；任务结束后用 release 释放。按操作指南自主完成：生成交接包、实施、运行真实测试、填写候选响应并调用 collab_submit_candidate 提交。完成后简要报告结果。`
	ccRevisionPrompt      = `协作中枢有任务需要返工。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 cc-haha）找到返工任务，读取 REVIEW 反馈和返工要求。若任务尚未认领工作区，先调用 collab_task_claim 认领并只在该工作区内返工；结束后释放。按指南重新生成交接包、实施返工并提交候选响应。完成后简要报告结果。`
	codexReviewPrompt     = `协作中枢有任务进入 REVIEW 需要你审查。请读取 collab://manual/agent-operating-guide 资源，然后调用 collab_get_next_work（client_id 用 codex）找到待审查任务，再调用 collab_get_task 读取任务详情、Evidence 和项目终审模式。若 allowed_actions 包含 approve/request_changes（agent 终审），生成交接包并提交 request_changes 或 approve 的候选响应；若不含（human 终审），只基于实际证据给出审查结论并说明等待人工批准，不要尝试提交 approve/request_changes 候选。完成后报告审查结论。`
	ccMessagePrompt       = `协作中枢有新的补充消息，需要你继续处理当前任务。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 cc-haha）找到你的任务，再调用 collab_get_task 读取任务详情、最新事件和补充消息。若任务尚未认领工作区，先调用 collab_task_claim 认领并只在该工作区内继续实施。结合新信息继续执行：生成交接包、实施、运行真实测试、填写候选响应并调用 collab_submit_candidate 提交。完成后简要报告结果。`
	codexMessagePrompt    = `协作中枢有新的补充消息需要你关注。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 codex）找到待审查任务，再调用 collab_get_task 读取任务详情、Evidence、补充消息和项目终审模式。结合新信息继续审查：若 allowed_actions 包含 approve/request_changes（agent 终审），生成交接包并提交对应候选响应；若不含（human 终审），给出审查结论并说明等待人工批准，不要尝试提交 approve/request_changes 候选。完成后报告结论。`
	reasonixReviewPrompt  = `协作中枢有任务进入 REVIEW 需要你审查。你是只读审查员：不得修改项目源码、配置、依赖、Git 状态或发布内容。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 reasonix）找到待审查任务，再调用 collab_get_task 读取任务详情、Evidence、验收标准和项目终审模式。只基于实际读取的文件、diff、测试证据给出结论；如需要返工，清楚列出可验证的问题。若 allowed_actions 包含 approve/request_changes（agent 终审），生成交接包并提交对应候选响应；若不含（human 终审），给出审查结论并说明等待人工批准，不要尝试提交 approve/request_changes 候选。`
	reasonixMessagePrompt = `协作中枢有新的补充消息需要你继续审查。你是只读审查员：不得修改项目源码、配置、依赖、Git 状态或发布内容。请读取 collab://manual/agent-operating-guide 资源，调用 collab_get_next_work（client_id 用 reasonix）找到待审查任务，再调用 collab_get_task 读取最新 Evidence、事件、补充消息和项目终审模式。只基于实际证据更新审查结论；仅当 allowed_actions 包含 approve/request_changes（agent 终审）时生成交接包并提交候选响应，human 终审项目则说明等待人工批准。`
)

// wakeRule maps a task state to the client that should be woken up.
type wakeRule struct {
	Client string
	Prompt string
}

func wakeRuleFor(status protocol.Status, responsible string) *wakeRule {
	switch status {
	case protocol.Assigned:
		if responsible == "cc-haha" {
			return &wakeRule{Client: "cc-haha", Prompt: ccExecutionPrompt}
		}
	case protocol.RevisionRequired:
		if responsible == "cc-haha" {
			return &wakeRule{Client: "cc-haha", Prompt: ccRevisionPrompt}
		}
	case protocol.Review:
		if responsible == "codex" {
			return &wakeRule{Client: "codex", Prompt: codexReviewPrompt}
		}
		if responsible == "reasonix" {
			return &wakeRule{Client: "reasonix", Prompt: reasonixReviewPrompt}
		}
	}
	return nil
}

// messageWakeRuleFor returns the client that should be woken when the latest
// event is a supplemental message for the responsible client. Done and
// blocked tasks never get woken for messages.
func messageWakeRuleFor(status protocol.Status, responsible string) *wakeRule {
	switch status {
	case protocol.Assigned, protocol.Working, protocol.RevisionRequired, protocol.Review:
	default:
		return nil
	}
	switch responsible {
	case "cc-haha":
		return &wakeRule{Client: "cc-haha", Prompt: ccMessagePrompt}
	case "codex":
		return &wakeRule{Client: "codex", Prompt: codexMessagePrompt}
	case "reasonix":
		return &wakeRule{Client: "reasonix", Prompt: reasonixMessagePrompt}
	}
	return nil
}

type wakeState struct {
	Notified      map[string]bool         `json:"notified"`
	WakeAt        map[string]time.Time    `json:"wake_at,omitempty"`
	Deliveries    map[string]wakeDelivery `json:"deliveries,omitempty"`
	DeliveryAudit []wakeDeliveryAudit     `json:"delivery_audit,omitempty"`
}

type wakeDeliveryStatus string

const (
	wakeDeliveryPrepared  wakeDeliveryStatus = "prepared"
	wakeDeliveryAccepted  wakeDeliveryStatus = "accepted"
	wakeDeliveryUncertain wakeDeliveryStatus = "uncertain"
	wakeDeliveryResolved  wakeDeliveryStatus = "manually_resolved"
)

// wakeDelivery is the durable boundary between the task journal and a native
// desktop conversation. A prepared record is written before any client call;
// after a crash it is treated as uncertain instead of replaying a turn that
// may already have reached the desktop client.
type wakeDelivery struct {
	ID        string             `json:"id"`
	TaskID    string             `json:"task_id"`
	Client    string             `json:"client"`
	Status    wakeDeliveryStatus `json:"status"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type wakeDeliveryResolution string

const (
	wakeDeliveryResolutionResolved  wakeDeliveryResolution = "resolved"
	wakeDeliveryResolutionAbandoned wakeDeliveryResolution = "abandoned"
)

// wakeDeliveryAudit records each human decision that releases an uncertain
// desktop handoff. It lives beside the delivery state so a retry can never be
// mistaken for an unreviewed automatic replay.
type wakeDeliveryAudit struct {
	DeliveryID string                 `json:"delivery_id"`
	TaskID     string                 `json:"task_id"`
	Client     string                 `json:"client"`
	Resolution wakeDeliveryResolution `json:"resolution"`
	Actor      string                 `json:"actor"`
	Note       string                 `json:"note"`
	At         time.Time              `json:"at"`
}

// wakeNotifier watches the task journal and wakes the responsible client when a
// task needs its next action. CC-HAHA and Codex retain their existing command
// paths; Reasonix uses its authenticated desktop bridge so the turn is visible
// in the user's native RE conversation.
type wakeNotifier struct {
	app                 *App
	interval            time.Duration
	dryRun              bool
	ccCommand           string
	codexCommand        string
	ccHahahaWorkProfile string
	reasonixWorkProfile string
	taskTimeout         time.Duration
	retryDelay          time.Duration
	stallTimeout        time.Duration
	statePath           string

	mu            sync.Mutex
	saveMu        sync.Mutex
	notified      map[string]bool
	wakeAt        map[string]time.Time
	running       map[string]bool
	retryAfter    map[string]time.Time
	deliveries    map[string]wakeDelivery
	deliveryAudit []wakeDeliveryAudit
}

func (a *App) watch(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	if len(args) > 0 && args[0] == "delivery" {
		return a.watchDelivery(ctx, args[1:], jsonOutput)
	}
	fs := newFlagSet("watch")
	interval := fs.Duration("interval", defaultWatchInterval, "")
	dryRun := fs.Bool("dry-run", false, "")
	once := fs.Bool("once", false, "")
	ccCommand := fs.String("cc-command", "claude", "")
	codexCommand := fs.String("codex-command", "codex", "")
	ccHahahaWorkProfile := fs.String("cc-work-profile", ccHahahaWorkDefault, "")
	reasonixWorkProfile := fs.String("reasonix-work-profile", reasonixWorkDelivery, "")
	taskTimeout := fs.Duration("task-timeout", 30*time.Minute, "")
	retryDelay := fs.Duration("retry-delay", 60*time.Second, "")
	stallTimeout := fs.Duration("stall-timeout", 4*time.Minute, "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	explicitFlags := map[string]bool{}
	fs.Visit(func(flag *flag.Flag) { explicitFlags[flag.Name] = true })
	// 统一协议层：未显式指定工作模式时，采用客户端注册表的默认声明。
	if !explicitFlags["cc-work-profile"] {
		if ccClient, readErr := a.Registry.ReadClient(ctx, "cc-haha"); readErr == nil && ccClient.DefaultProfile != "" {
			*ccHahahaWorkProfile = resolveClientDefault(*ccHahahaWorkProfile, explicitFlags["cc-work-profile"], ccClient.DefaultProfile)
		}
	}
	if !explicitFlags["reasonix-work-profile"] {
		if reClient, readErr := a.Registry.ReadClient(ctx, "reasonix"); readErr == nil && reClient.DefaultProfile != "" {
			*reasonixWorkProfile = resolveClientDefault(*reasonixWorkProfile, explicitFlags["reasonix-work-profile"], reClient.DefaultProfile)
		}
	}
	normalizedReasonixWorkProfile, err := normalizeReasonixWorkProfile(*reasonixWorkProfile)
	if err != nil {
		return ExitValidation, fmt.Errorf("--reasonix-work-profile %w", err)
	}
	normalizedCCHahaWorkProfile, err := normalizeCCHahaWorkProfile(*ccHahahaWorkProfile)
	if err != nil {
		return ExitValidation, fmt.Errorf("--cc-work-profile %w", err)
	}
	notifier := &wakeNotifier{
		app:                 a,
		interval:            *interval,
		dryRun:              *dryRun,
		ccCommand:           *ccCommand,
		codexCommand:        *codexCommand,
		ccHahahaWorkProfile: normalizedCCHahaWorkProfile,
		reasonixWorkProfile: normalizedReasonixWorkProfile,
		taskTimeout:         *taskTimeout,
		retryDelay:          *retryDelay,
		stallTimeout:        *stallTimeout,
		statePath:           filepath.Join(a.Root, "collaboration", ".runtime", wakeStateFileName),
		notified:            map[string]bool{},
		wakeAt:              map[string]time.Time{},
		running:             map[string]bool{},
		retryAfter:          map[string]time.Time{},
		deliveries:          map[string]wakeDelivery{},
	}
	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if !notifier.dryRun {
		stateLock, err := notifier.lockState(watchCtx)
		if err != nil {
			return ExitConflict, errors.New("watch state is already in use; stop the running watcher before starting another one")
		}
		defer stateLock.Unlock()
	}
	if err := notifier.load(); err != nil {
		return exitCode(err), err
	}
	defer notifier.save()
	fmt.Fprintf(a.Stdout, "collab watch started (interval=%s dry_run=%t cc=%s codex=%s cc-haha=desktop(normal/%s/ask) reasonix=desktop(normal/%s/auto))\n", notifier.interval, notifier.dryRun, notifier.ccCommand, notifier.codexCommand, notifier.ccHahahaWorkProfile, notifier.reasonixWorkProfile)
	notifier.scan(watchCtx)
	if *once {
		fmt.Fprintln(a.Stdout, "collab watch completed one scan")
		return ExitOK, nil
	}
	ticker := time.NewTicker(notifier.interval)
	defer ticker.Stop()
	for {
		select {
		case <-watchCtx.Done():
			fmt.Fprintln(a.Stdout, "collab watch stopped")
			return ExitOK, nil
		case <-ticker.C:
			notifier.scan(watchCtx)
		}
	}
}

type wakeDeliveryResolutionOutput struct {
	DeliveryID   string                 `json:"delivery_id"`
	TaskID       string                 `json:"task_id"`
	Client       string                 `json:"client"`
	Resolution   wakeDeliveryResolution `json:"resolution"`
	Actor        string                 `json:"actor"`
	Note         string                 `json:"note"`
	At           time.Time              `json:"at"`
	RetryAllowed bool                   `json:"retry_allowed"`
}

// watchDelivery gives a human operator a deliberate release path for an
// uncertain desktop handoff. It cannot run alongside watch, so a decision is
// never applied while a delivery is still in flight.
func (a *App) watchDelivery(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	if len(args) == 0 {
		return ExitValidation, errUsage
	}
	var resolution wakeDeliveryResolution
	switch args[0] {
	case "resolve":
		resolution = wakeDeliveryResolutionResolved
	case "abandon":
		resolution = wakeDeliveryResolutionAbandoned
	default:
		return ExitValidation, errUsage
	}
	fs := newFlagSet("watch delivery " + string(resolution))
	deliveryID := fs.String("delivery", "", "")
	actor := fs.String("actor", "", "")
	note := fs.String("note", "", "")
	if err := parse(fs, args[1:]); err != nil {
		return ExitValidation, err
	}
	if err := require("delivery", *deliveryID, "actor", *actor, "note", *note); err != nil || !protocol.IsValidID(*actor) || len(strings.TrimSpace(*note)) > 512 {
		return ExitValidation, errUsage
	}

	notifier := &wakeNotifier{
		app:           a,
		statePath:     filepath.Join(a.Root, "collaboration", ".runtime", wakeStateFileName),
		notified:      map[string]bool{},
		wakeAt:        map[string]time.Time{},
		running:       map[string]bool{},
		retryAfter:    map[string]time.Time{},
		deliveries:    map[string]wakeDelivery{},
		deliveryAudit: []wakeDeliveryAudit{},
	}
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stateLock, err := notifier.lockState(lockCtx)
	if err != nil {
		return ExitConflict, errors.New("watch is running; stop it before resolving a desktop delivery")
	}
	defer stateLock.Unlock()
	if err := notifier.load(); err != nil {
		return exitCode(err), err
	}
	audit, err := notifier.resolveUncertainDelivery(*deliveryID, resolution, *actor, *note)
	if err != nil {
		return ExitValidation, err
	}
	if err := notifier.save(); err != nil {
		return exitCode(err), err
	}
	output := wakeDeliveryResolutionOutput{
		DeliveryID:   audit.DeliveryID,
		TaskID:       audit.TaskID,
		Client:       audit.Client,
		Resolution:   audit.Resolution,
		Actor:        audit.Actor,
		Note:         audit.Note,
		At:           audit.At,
		RetryAllowed: audit.Resolution == wakeDeliveryResolutionAbandoned,
	}
	if jsonOutput {
		a.writeJSON(output)
	} else {
		fmt.Fprintf(a.Stdout, "delivery_id: %s\ntask_id: %s\nclient: %s\nresolution: %s\nactor: %s\nretry_allowed: %t\n", output.DeliveryID, output.TaskID, output.Client, output.Resolution, output.Actor, output.RetryAllowed)
	}
	return ExitOK, nil
}

// resolveClientDefault applies the unified protocol layer precedence:
// an explicit CLI flag beats the client registry default, which beats the
// built-in fallback.
func resolveClientDefault(value string, explicit bool, registered string) string {
	if explicit || registered == "" {
		return value
	}
	return registered
}

func (n *wakeNotifier) scan(ctx context.Context) {
	taskIDs, err := n.app.Journal.ListTaskIDs(ctx)
	if err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] list tasks: %v\n", err)
		return
	}
	for _, taskID := range taskIDs {
		snapshot, err := n.app.Query.Snapshot(ctx, taskID, 0)
		if err != nil {
			fmt.Fprintf(n.app.Stderr, "[watch] read task %s: %v\n", taskID, err)
			continue
		}
		key := wakeKey(snapshot)
		rule, prompt := wakeRuleAndPrompt(snapshot)
		if rule == nil {
			n.markNotified(key)
			continue
		}
		n.mu.Lock()
		if n.wakeAt == nil {
			n.wakeAt = map[string]time.Time{}
		}
		if n.deliveries == nil {
			n.deliveries = map[string]wakeDelivery{}
		}
		if delivery, exists := n.deliveries[key]; exists {
			if delivery.Status == wakeDeliveryPrepared && !n.running[delivery.Client] {
				delivery.Status = wakeDeliveryUncertain
				delivery.UpdatedAt = n.app.Clock()
				n.deliveries[key] = delivery
			}
			// A prepared record survived a process interruption, or the desktop
			// client explicitly left the result uncertain. Replaying could create
			// a second user turn, so hold this task for visible/manual resolution.
			n.notified[key] = true
			if _, tracked := n.wakeAt[key]; !tracked {
				n.wakeAt[key] = delivery.UpdatedAt
			}
			n.mu.Unlock()
			continue
		}
		if n.notified[key] {
			if wokenAt, tracked := n.wakeAt[key]; tracked && n.app.Clock().Sub(wokenAt) < n.stallTimeout {
				n.mu.Unlock()
				continue
			}
			// A desktop-owned conversation cannot be waited on directly. Retry only
			// after its bounded stall window, never on the next scan.
			delete(n.notified, key)
			delete(n.wakeAt, key)
		}
		if until, exists := n.retryAfter[key]; exists && n.app.Clock().Before(until) {
			n.mu.Unlock()
			continue
		}
		if n.running[rule.Client] {
			n.mu.Unlock()
			fmt.Fprintf(n.app.Stdout, "[watch] %s: %s busy, will re-check %s later\n", time.Now().UTC().Format(time.RFC3339), rule.Client, taskID)
			continue
		}
		n.running[rule.Client] = true
		n.notified[key] = true
		n.wakeAt[key] = n.app.Clock()
		var delivery wakeDelivery
		if usesReliableDesktopDelivery(rule.Client) {
			delivery = wakeDelivery{
				ID:        wakeDeliveryID(key, rule.Client),
				TaskID:    snapshot.Task.ID,
				Client:    rule.Client,
				Status:    wakeDeliveryPrepared,
				UpdatedAt: n.app.Clock(),
			}
			n.deliveries[key] = delivery
		}
		n.mu.Unlock()
		if n.dryRun {
			fmt.Fprintf(n.app.Stdout, "[watch] %s: WOULD wake %s for %s (%s)\n", time.Now().UTC().Format(time.RFC3339), rule.Client, taskID, snapshot.State.Status)
			n.mu.Lock()
			delete(n.running, rule.Client)
			delete(n.wakeAt, key)
			delete(n.deliveries, key)
			n.mu.Unlock()
			continue
		}
		if usesReliableDesktopDelivery(rule.Client) {
			if err := n.save(); err != nil {
				n.mu.Lock()
				delete(n.running, rule.Client)
				delete(n.notified, key)
				delete(n.wakeAt, key)
				delete(n.deliveries, key)
				n.mu.Unlock()
				fmt.Fprintf(n.app.Stderr, "[watch] persist delivery %s for %s: %v\n", delivery.ID, snapshot.Task.ID, err)
				continue
			}
		}
		go n.wake(ctx, rule, snapshot, prompt, key, delivery.ID)
	}
	if err := n.save(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] persist wake state: %v\n", err)
	}
}

// wakeRuleAndPrompt decides whether a snapshot needs a wake and what prompt
// to send. A supplemental message for the responsible client re-wakes the
// same client with the message body attached; the task keeps its dedicated
// session, so the follow-up continues the same conversation.
func wakeRuleAndPrompt(snapshot store.TaskSnapshot) (*wakeRule, string) {
	rule := wakeRuleFor(snapshot.State.Status, snapshot.State.ResponsibleClient)
	supplement := ""
	if last := lastEvent(snapshot.Events); last != nil && last.Type == protocol.EventMessageAdded && last.Actor != snapshot.State.ResponsibleClient {
		if rule == nil {
			rule = messageWakeRuleFor(snapshot.State.Status, snapshot.State.ResponsibleClient)
		}
		supplement = strings.TrimSpace(last.Body)
	}
	if rule == nil {
		return nil, ""
	}
	prompt := rule.Prompt + "\n任务：" + snapshot.Task.ID
	prompt += taskProgressSummary(snapshot)
	if supplement != "" {
		prompt += "\n\n补充消息：" + supplement
	}
	return rule, prompt
}

// taskProgressSummary renders a compact digest of the task journal so a
// desktop conversation keeps working even when the client's own memory or
// compaction did not preserve the earlier turns. Event bodies are untrusted
// data and are flattened and truncated before inclusion.
func taskProgressSummary(snapshot store.TaskSnapshot) string {
	const limit = 15
	events := snapshot.Events
	if len(events) == 0 {
		return ""
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "\n\n任务进展摘要（共 %d 条事件，显示最近 %d 条）：", len(events), min(limit, len(events)))
	start := 0
	if len(events) > limit {
		start = len(events) - limit
	}
	for _, event := range events[start:] {
		fmt.Fprintf(&builder, "\n- %s（%s，%s）", eventTypeLabel(event.Type), event.Actor, event.At.Local().Format("01-02 15:04"))
		if event.Type == protocol.EventAssigned && event.TargetClient != "" {
			builder.WriteString(" → " + event.TargetClient)
		}
		if body := compactEventBody(event.Body); body != "" {
			builder.WriteString("：" + body)
		}
	}
	fmt.Fprintf(&builder, "\n当前状态：%s，责任方：%s，版本 v%d", snapshot.State.Status, snapshot.State.ResponsibleClient, snapshot.State.Version)
	return builder.String()
}

func eventTypeLabel(eventType protocol.EventType) string {
	switch eventType {
	case protocol.EventTaskCreated:
		return "创建"
	case protocol.EventAssigned:
		return "指派"
	case protocol.EventAccepted:
		return "接受"
	case protocol.EventMessageAdded:
		return "消息"
	case protocol.EventEvidenceAdded:
		return "证据"
	case protocol.EventSubmitted:
		return "提交"
	case protocol.EventChangesRequested:
		return "要求返工"
	case protocol.EventRevisionStarted:
		return "返工开始"
	case protocol.EventApproved:
		return "批准"
	case protocol.EventBlocked:
		return "阻塞"
	default:
		return string(eventType)
	}
}

func compactEventBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > 80 {
		return body[:80] + "…"
	}
	return body
}

func (n *wakeNotifier) wake(ctx context.Context, rule *wakeRule, snapshot store.TaskSnapshot, prompt, key, deliveryID string) {
	retryIfUnchanged := true
	defer func() {
		n.mu.Lock()
		delete(n.running, rule.Client)
		n.mu.Unlock()
		if retryIfUnchanged {
			n.allowRetryIfUnchanged(ctx, snapshot, rule)
		}
	}()
	prompt = withWorktreePrompt(ctx, prompt, snapshot, newWorktreeRegistry(n.app.Root))
	workDir := n.app.Root
	if binding, err := n.app.Bindings.ReadBinding(ctx, DefaultDeviceID(), snapshot.Project.ID); err == nil {
		if info, statErr := os.Stat(binding.LocalPath); statErr == nil && info.IsDir() {
			workDir = binding.LocalPath
		}
	}
	if rule.Client == "cc-haha" {
		if err := n.wakeCCHahaDesktop(ctx, snapshot.Task.ID, workDir, prompt, deliveryID); err != nil {
			fmt.Fprintf(n.app.Stderr, "[watch] %s: CC-HAHA wake failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, err)
			if isUncertainDelivery(err) {
				n.markDelivery(key, wakeDeliveryUncertain)
				retryIfUnchanged = false
				fmt.Fprintf(n.app.Stderr, "[watch] %s: CC-HAHA delivery %s for %s is unconfirmed; verify the visible session before manually resolving it\n", time.Now().UTC().Format(time.RFC3339), deliveryID, snapshot.Task.ID)
			}
		} else {
			n.markDelivery(key, wakeDeliveryAccepted)
			retryIfUnchanged = false
		}
		return
	}
	if rule.Client == "reasonix" {
		if err := n.wakeReasonixDesktop(ctx, snapshot.Task.ID, workDir, prompt, deliveryID); err != nil {
			fmt.Fprintf(n.app.Stderr, "[watch] %s: Reasonix desktop wake failed for %s: %v\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID, err)
			if isUncertainDelivery(err) {
				n.markDelivery(key, wakeDeliveryUncertain)
				retryIfUnchanged = false
				fmt.Fprintf(n.app.Stderr, "[watch] %s: Reasonix delivery %s for %s is unconfirmed; verify the visible conversation before manually resolving it\n", time.Now().UTC().Format(time.RFC3339), deliveryID, snapshot.Task.ID)
			}
		} else {
			n.markDelivery(key, wakeDeliveryAccepted)
			retryIfUnchanged = false
			fmt.Fprintf(n.app.Stdout, "[watch] %s: sent %s to the visible Reasonix conversation\n", time.Now().UTC().Format(time.RFC3339), snapshot.Task.ID)
		}
		return
	}
	command, args := n.codexCommand, []string{"exec", "-p"}
	env := os.Environ()
	cmd := exec.Command(command, append(args, prompt)...)
	cmd.Dir = workDir
	cmd.Env = env
	cmd.Stdout = n.app.Stdout
	cmd.Stderr = n.app.Stderr
	hideWatchCommandWindow(cmd)
	fmt.Fprintf(n.app.Stdout, "[watch] %s: wake %s for %s (cwd=%s)\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID, workDir)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] %s: failed to start %s for %s: %v\n", time.Now().UTC().Format(time.RFC3339), command, snapshot.Task.ID, err)
		return
	}
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] %s: %s finished %s with error: %v\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID, err)
		return
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: %s finished %s\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID)
}

// withWorktreePrompt appends the claimed worktree to a wake prompt so the
// desktop client works in the registered isolated directory instead of
// guessing a location shared with another agent.
func withWorktreePrompt(ctx context.Context, prompt string, snapshot store.TaskSnapshot, registry *worktreeRegistry) string {
	if registry == nil {
		return prompt
	}
	worktree, ok, err := registry.Get(ctx, snapshot.Task.ID)
	if err != nil || !ok {
		return prompt
	}
	return prompt + fmt.Sprintf("\n\n工作区登记：%s（认领者：%s）", worktree.Worktree, worktree.ClaimedBy)
}

func lastEvent(events []protocol.Event) *protocol.Event {
	if len(events) == 0 {
		return nil
	}
	return &events[len(events)-1]
}

// ccSessionNamespace is the UUIDv5 namespace used to derive a stable CC-HAHA
// session ID from a collaboration task ID. One task always maps to the same
// session so follow-up messages resume the same conversation.
var ccSessionNamespace = [16]byte{0x8a, 0x9b, 0x2c, 0x3d, 0x4e, 0x5f, 0x4a, 0x6b, 0x8c, 0x7d, 0x9e, 0x0f, 0x1a, 0x2b, 0x3c, 0x4d}

// ccSessionUUID derives a deterministic UUIDv5 for a task. It matches the
// session ID used for --session-id (first wake) and --resume (later wakes).
func ccSessionUUID(taskID string) string {
	hash := sha1.New()
	_, _ = io.WriteString(hash, string(ccSessionNamespace[:]))
	_, _ = io.WriteString(hash, taskID)
	sum := hash.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func wakeKey(snapshot store.TaskSnapshot) string {
	return fmt.Sprintf("%s|%s|%d", snapshot.Task.ID, snapshot.State.Status, snapshot.State.Version)
}

func usesReliableDesktopDelivery(client string) bool {
	return client == "cc-haha" || client == "reasonix"
}

// wakeDeliveryID is stable for one task-state transition and target client.
// Retrying the same handoff therefore carries the same idempotency key to the
// desktop client instead of creating another visible user message.
func wakeDeliveryID(key, client string) string {
	sum := sha256.Sum256([]byte("collab-wake-v1\x00" + client + "\x00" + key))
	return "delivery-" + fmt.Sprintf("%x", sum[:16])
}

type uncertainDeliveryError struct{ err error }

func (e *uncertainDeliveryError) Error() string { return e.err.Error() }
func (e *uncertainDeliveryError) Unwrap() error { return e.err }

func uncertainDelivery(err error) error {
	if err == nil {
		return nil
	}
	return &uncertainDeliveryError{err: err}
}

func isUncertainDelivery(err error) bool {
	var uncertain *uncertainDeliveryError
	return errors.As(err, &uncertain)
}

func unsupportedCCHahaDesktopDelivery() error {
	return uncertainDelivery(errors.New("CC-HAHA desktop delivery is unsupported on this platform because no matching delivery acknowledgement is available"))
}

func validateCCHahaDeliveryAck(deliveryID, acknowledgedID, state string) error {
	if acknowledgedID != deliveryID {
		return uncertainDelivery(errors.New("CC-HAHA acknowledged a different delivery"))
	}
	if state != "accepted" {
		return uncertainDelivery(fmt.Errorf("CC-HAHA delivery is %s", state))
	}
	return nil
}

func (n *wakeNotifier) markDelivery(key string, status wakeDeliveryStatus) {
	n.mu.Lock()
	delivery, ok := n.deliveries[key]
	if ok {
		delivery.Status = status
		delivery.UpdatedAt = n.app.Clock()
		n.deliveries[key] = delivery
	}
	n.mu.Unlock()
	if err := n.save(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] persist delivery %s: %v\n", key, err)
	}
}

func (n *wakeNotifier) resolveUncertainDelivery(deliveryID string, resolution wakeDeliveryResolution, actor, note string) (wakeDeliveryAudit, error) {
	deliveryID = strings.TrimSpace(deliveryID)
	actor = strings.TrimSpace(actor)
	note = strings.TrimSpace(note)
	if deliveryID == "" || !protocol.IsValidID(actor) || note == "" || len(note) > 512 {
		return wakeDeliveryAudit{}, errors.New("delivery resolution requires a delivery, valid actor, and note")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensureStateMapsLocked()
	var key string
	var delivery wakeDelivery
	for candidateKey, candidate := range n.deliveries {
		if candidate.ID != deliveryID {
			continue
		}
		if key != "" {
			return wakeDeliveryAudit{}, errors.New("delivery id is ambiguous")
		}
		key, delivery = candidateKey, candidate
	}
	if key == "" {
		return wakeDeliveryAudit{}, errors.New("delivery is not pending manual resolution")
	}
	if delivery.Status != wakeDeliveryUncertain {
		return wakeDeliveryAudit{}, errors.New("only an uncertain delivery can be resolved or abandoned")
	}
	audit := wakeDeliveryAudit{
		DeliveryID: delivery.ID,
		TaskID:     delivery.TaskID,
		Client:     delivery.Client,
		Resolution: resolution,
		Actor:      actor,
		Note:       note,
		At:         n.app.now(),
	}
	switch resolution {
	case wakeDeliveryResolutionResolved:
		delivery.Status = wakeDeliveryResolved
		delivery.UpdatedAt = audit.At
		n.deliveries[key] = delivery
	case wakeDeliveryResolutionAbandoned:
		delete(n.deliveries, key)
		delete(n.notified, key)
		delete(n.wakeAt, key)
		delete(n.retryAfter, key)
	default:
		return wakeDeliveryAudit{}, errors.New("delivery resolution is invalid")
	}
	n.deliveryAudit = append(n.deliveryAudit, audit)
	return audit, nil
}

func (n *wakeNotifier) lockState(ctx context.Context) (store.Lock, error) {
	if strings.TrimSpace(n.statePath) == "" {
		return nil, errors.New("watch state path is required")
	}
	return (store.FlockLocker{}).Lock(ctx, filepath.Join(filepath.Dir(n.statePath), "locks", wakeStateLockFileName))
}

func (n *wakeNotifier) ensureStateMapsLocked() {
	if n.notified == nil {
		n.notified = map[string]bool{}
	}
	if n.wakeAt == nil {
		n.wakeAt = map[string]time.Time{}
	}
	if n.running == nil {
		n.running = map[string]bool{}
	}
	if n.retryAfter == nil {
		n.retryAfter = map[string]time.Time{}
	}
	if n.deliveries == nil {
		n.deliveries = map[string]wakeDelivery{}
	}
}

// allowRetryIfUnchanged removes the notified marker when the task is still in
// the same state after a wake attempt, so a later scan can try again. If the
// task moved forward, the marker stays and the new state drives the next wake.
func (n *wakeNotifier) allowRetryIfUnchanged(ctx context.Context, snapshot store.TaskSnapshot, rule *wakeRule) {
	current, err := n.app.Query.Snapshot(ctx, snapshot.Task.ID, 0)
	if err != nil {
		return
	}
	if current.State.Status != snapshot.State.Status || current.State.Version != snapshot.State.Version {
		return
	}
	if wakeRuleFor(current.State.Status, current.State.ResponsibleClient) == nil {
		return
	}
	key := wakeKey(current)
	n.mu.Lock()
	delete(n.notified, key)
	delete(n.wakeAt, key)
	delete(n.deliveries, key)
	n.retryAfter[key] = n.app.Clock().Add(n.retryDelay)
	n.mu.Unlock()
	if err := n.save(); err != nil {
		fmt.Fprintf(n.app.Stderr, "[watch] persist retry state for %s: %v\n", snapshot.Task.ID, err)
	}
	fmt.Fprintf(n.app.Stdout, "[watch] %s: %s did not advance %s; will retry in %s\n", time.Now().UTC().Format(time.RFC3339), rule.Client, snapshot.Task.ID, n.retryDelay)
}

func (n *wakeNotifier) markNotified(key string) {
	n.mu.Lock()
	if n.wakeAt == nil {
		n.wakeAt = map[string]time.Time{}
	}
	n.notified[key] = true
	n.wakeAt[key] = n.app.Clock()
	n.mu.Unlock()
}

// ccSessionID returns the CC-HAHA session persisted for a task, or empty.
func (n *wakeNotifier) ccSessionID(taskID string) string {
	data, err := os.ReadFile(filepath.Join(n.app.Root, "collaboration", ".runtime", ccSessionsFileName))
	if err != nil {
		return ""
	}
	var sessions struct {
		Sessions map[string]string `json:"sessions"`
	}
	if json.Unmarshal(data, &sessions) != nil || sessions.Sessions == nil {
		return ""
	}
	return sessions.Sessions[taskID]
}

// setCCSessionID persists the task-to-session mapping so follow-up messages
// resume the same CC-HAHA conversation.
func (n *wakeNotifier) setCCSessionID(taskID, sessionID string) error {
	path := filepath.Join(n.app.Root, "collaboration", ".runtime", ccSessionsFileName)
	state := struct {
		Sessions map[string]string `json:"sessions"`
	}{Sessions: map[string]string{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]string{}
	}
	state.Sessions[taskID] = sessionID
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (n *wakeNotifier) load() error {
	data, err := os.ReadFile(n.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state wakeState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	n.mu.Lock()
	n.ensureStateMapsLocked()
	if state.Notified != nil {
		n.notified = state.Notified
	}
	if state.WakeAt != nil {
		n.wakeAt = state.WakeAt
	}
	if state.Deliveries != nil {
		n.deliveries = state.Deliveries
		for key, delivery := range n.deliveries {
			if delivery.Status == wakeDeliveryPrepared {
				delivery.Status = wakeDeliveryUncertain
				delivery.UpdatedAt = n.app.Clock()
				n.deliveries[key] = delivery
			}
		}
	}
	if state.DeliveryAudit != nil {
		n.deliveryAudit = append([]wakeDeliveryAudit(nil), state.DeliveryAudit...)
	}
	n.mu.Unlock()
	return nil
}

func (n *wakeNotifier) save() error {
	if n.dryRun {
		return nil
	}
	n.saveMu.Lock()
	defer n.saveMu.Unlock()
	n.mu.Lock()
	n.ensureStateMapsLocked()
	state := wakeState{
		Notified:      make(map[string]bool, len(n.notified)),
		WakeAt:        make(map[string]time.Time, len(n.wakeAt)),
		Deliveries:    make(map[string]wakeDelivery, len(n.deliveries)),
		DeliveryAudit: append([]wakeDeliveryAudit(nil), n.deliveryAudit...),
	}
	for key := range n.notified {
		state.Notified[key] = true
	}
	for key, value := range n.wakeAt {
		state.WakeAt[key] = value
	}
	for key, value := range n.deliveries {
		state.Deliveries[key] = value
	}
	n.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(n.statePath), 0o700); err != nil {
		return err
	}
	temp := n.statePath + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, n.statePath)
}
