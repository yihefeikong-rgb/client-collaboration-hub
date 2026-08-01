(() => {
  "use strict";

  const STATE_LABELS = {
    DRAFT: "草稿",
    ASSIGNED: "已分配",
    WORKING: "执行中",
    REVIEW: "待审查",
    REVISION_REQUIRED: "需要返工",
    DONE: "已完成",
    BLOCKED: "已阻塞",
    CORRUPT: "数据损坏",
    RECOVERABLE_TAIL: "需要恢复",
  };

  const ACTION_LABELS = {
    request_changes: "要求返工",
    approve: "批准完成",
    message: "发送补充消息",
  };

  const ACTION_PRIORITY = ["approve", "request_changes"];
  const FILTERED_STATES = new Set(["REVIEW", "WORKING", "REVISION_REQUIRED", "BLOCKED"]);
  const FINAL_REVIEW_LABELS = {
    human: "人工终审",
    agent: "Agent 终审",
  };

  let csrfToken = "";
  let overview = null;
  let currentTask = null;
  let activeFilter = "REVIEW";
  let selectedProject = "";
  let pendingConfirmation = null;

  const byID = (id) => document.getElementById(id);

  function text(value) {
    return value === undefined || value === null ? "" : String(value);
  }

  function lines(value) {
    return text(value).split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
  }

  function field(form, name) {
    const control = form.elements.namedItem(name);
    return control ? text(control.value).trim() : "";
  }

  function currentQuery() {
    return "";
  }

  function statusClass(value) {
    return `state-${text(value).replace(/[^A-Z_]/g, "") || "DRAFT"}`;
  }

  function healthClass(value) {
    return `health-${text(value).replace(/[^A-Z_]/g, "") || "UNKNOWN"}`;
  }

  function formatTime(value) {
    const date = new Date(value);
    if (!value || Number.isNaN(date.getTime())) return "—";
    return new Intl.DateTimeFormat("zh-CN", {
      month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false,
    }).format(date);
  }

  function setStatus(message, kind = "") {
    const node = byID("status");
    node.textContent = message;
    node.className = `status-line ${kind}`.trim();
  }

  function showResult(value) {
    byID("result-panel").hidden = false;
    byID("output").textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  }

  function clearResult() {
    byID("result-panel").hidden = true;
    byID("output").textContent = "等待操作。";
  }

  async function request(method, path, payload) {
    const headers = { Accept: "application/json" };
    const options = { method, headers, cache: "no-store" };
    if (method !== "GET") {
      headers["Content-Type"] = "application/json";
      headers["X-Collab-CSRF"] = csrfToken;
      options.body = JSON.stringify(payload || {});
    }
    const response = await fetch(path, options);
    const body = await response.json().catch(() => ({ error: "本地服务返回了无效 JSON。" }));
    if (!response.ok) throw new Error(body.error || `请求失败（${response.status}）`);
    return body;
  }

  function clientName(id) {
    if (!id) return "—";
    const match = overview && overview.clients.find((client) => client.id === id);
    return match ? match.name : id;
  }

  function projectName(id) {
    const match = overview && overview.projects.find((project) => project.id === id);
    return match ? match.name : (id || "—");
  }

  function selectedProjectView() {
    if (!overview || !selectedProject) return null;
    return overview.projects.find((project) => project.id === selectedProject) || null;
  }

  function finalReviewLabel(value) {
    return FINAL_REVIEW_LABELS[value] || "—";
  }

  function nextStep(task) {
    switch (task.status) {
      case "DRAFT": return { title: "等待任务分配", description: "Codex 通过本地协作命令创建和分配任务；网页不替它操作。" };
      case "ASSIGNED": return { title: "等待执行客户端接受", description: "CC-HAHA 在自己的原生会话中接受任务。" };
      case "WORKING": return { title: "等待代理提交审查材料", description: "执行客户端提交 diff、test 等 Evidence 后，任务才会进入你的审核队列。" };
      case "REVIEW": return { title: "由你做最终审核", description: "阅读 Evidence 后，选择批准完成或要求返工。" };
      case "REVISION_REQUIRED": return { title: "等待代理返工", description: "CC-HAHA 恢复执行并补充新的 Evidence。" };
      case "BLOCKED": return { title: "等待解除阻塞", description: "中枢不会自动重试；先记录或解决解除条件。" };
      case "DONE": return { title: "任务已完成", description: "可查看最终 Evidence 与交接历史；不会再写入任务。" };
      default: return { title: "检查任务健康度", description: task.reason || "任务需要先完成数据诊断。" };
    }
  }

  function actionDescription(action) {
    const descriptions = {
      request_changes: "以结构化反馈要求执行者返工。",
      approve: "形成不可逆的完成审查结论。",
      message: "向当前负责客户端发送一条补充消息；不改变任务状态，watch 会自动唤醒同一会话。",
    };
    return descriptions[action] || "执行此操作。";
  }

  function clearNode(node) {
    node.replaceChildren();
  }

  function setText(id, value, fallback = "—") {
    byID(id).textContent = text(value) || fallback;
  }

  function makeStatus(value) {
    const label = document.createElement("span");
    label.className = `status-label ${statusClass(value)}`;
    label.textContent = STATE_LABELS[value] || value || "—";
    return label;
  }

  function renderCounts(tasks) {
    const counts = { ALL: tasks.length, REVIEW: 0, WORKING: 0, REVISION_REQUIRED: 0, BLOCKED: 0 };
    for (const task of tasks) {
      if (FILTERED_STATES.has(task.status)) counts[task.status] += 1;
    }
    byID("count-all").textContent = counts.ALL;
    byID("count-review").textContent = counts.REVIEW;
    byID("count-working").textContent = counts.WORKING;
    byID("count-revision").textContent = counts.REVISION_REQUIRED;
    byID("count-blocked").textContent = counts.BLOCKED;
  }

  function visibleTasks() {
    if (!overview) return [];
    const query = byID("task-search").value.trim().toLowerCase();
    return overview.tasks.filter((task) => {
      const matchesStatus = activeFilter === "ALL" || task.status === activeFilter;
      const matchesProject = !selectedProject || task.project_id === selectedProject;
      const haystack = `${task.id} ${task.title} ${task.project_id}`.toLowerCase();
      return matchesStatus && matchesProject && (!query || haystack.includes(query));
    });
  }

  function renderQueue() {
    const tasks = visibleTasks();
    const body = byID("tasks");
    clearNode(body);
    byID("queue-description").textContent = `${tasks.length} 个任务符合当前视图。`;
    byID("queue-empty").hidden = tasks.length !== 0;

    for (const task of tasks) {
      const row = document.createElement("tr");
      const taskCell = document.createElement("td");
      const open = document.createElement("button");
      open.type = "button";
      open.className = "task-open";
      open.setAttribute("aria-label", `打开任务 ${task.id}`);
      const title = document.createElement("span");
      title.className = "task-title";
      title.textContent = task.title || task.id;
      const id = document.createElement("span");
      id.className = "task-id";
      id.textContent = task.id;
      open.append(title, id);
      open.addEventListener("click", () => loadTask(task.id));
      taskCell.appendChild(open);
      row.appendChild(taskCell);

      const stateCell = document.createElement("td");
      stateCell.appendChild(makeStatus(task.status));
      row.appendChild(stateCell);

      const responsibility = document.createElement("td");
      responsibility.className = "table-muted";
      responsibility.textContent = clientName(task.responsible_client);
      row.appendChild(responsibility);

      const next = document.createElement("td");
      next.className = "table-muted";
      next.textContent = nextStep(task).title;
      row.appendChild(next);

      const revision = document.createElement("td");
      revision.className = "mono";
      revision.textContent = `v${task.version} · #${task.last_event_id}`;
      row.appendChild(revision);

      const health = document.createElement("td");
      health.className = `table-muted ${healthClass(task.health)}`;
      health.textContent = task.health || "UNKNOWN";
      row.appendChild(health);
      body.appendChild(row);
    }
  }

  function attentionRank(task) {
    const ranks = { REVIEW: 0, REVISION_REQUIRED: 1, BLOCKED: 2, WORKING: 3, ASSIGNED: 4, DRAFT: 5, DONE: 6 };
    return ranks[task.status] ?? 7;
  }

  function renderAttention(tasks) {
    const list = byID("attention-list");
    clearNode(list);
    const attention = tasks.filter((task) => task.status !== "DONE").sort((left, right) => attentionRank(left) - attentionRank(right)).slice(0, 4);
    if (!attention.length) {
      const item = document.createElement("li");
      item.textContent = "当前没有需要处理的未完成任务。";
      list.appendChild(item);
      return;
    }
    for (const task of attention) {
      const item = document.createElement("li");
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = `${task.id} · ${nextStep(task).title}`;
      button.addEventListener("click", () => loadTask(task.id));
      const detail = document.createElement("span");
      detail.textContent = `${STATE_LABELS[task.status] || task.status} · ${clientName(task.responsible_client)}`;
      item.append(button, detail);
      list.appendChild(item);
    }
  }

  function renderDefinitionList(target, values, format) {
    const list = byID(target);
    clearNode(list);
    if (!values.length) {
      const item = document.createElement("li");
      item.textContent = "暂无已登记记录。";
      list.appendChild(item);
      return;
    }
    for (const value of values) {
      const item = document.createElement("li");
      const primary = document.createElement("strong");
      const secondary = document.createElement("span");
      [primary.textContent, secondary.textContent] = format(value);
      item.append(primary, secondary);
      list.appendChild(item);
    }
  }

  function renderSetup() {
    if (!overview) return;
    renderDefinitionList("client-list", overview.clients, (client) => [client.name, `${client.id} · ${client.capabilities.join(", ") || "未声明能力"}`]);
    renderDefinitionList("project-list", overview.projects, (project) => [project.name, `${project.id}${project.created_at ? ` · ${formatTime(project.created_at)}` : ""}`]);
  }

  function renderProjectSelector() {
    const select = byID("project-select");
    const available = new Set((overview.projects || []).map((project) => project.id));
    if (selectedProject && !available.has(selectedProject)) selectedProject = "";
    clearNode(select);
    const all = document.createElement("option");
    all.value = "";
    all.textContent = "全部项目";
    select.appendChild(all);
    for (const project of overview.projects || []) {
      const option = document.createElement("option");
      option.value = project.id;
      option.textContent = project.name;
      select.appendChild(option);
    }
    select.value = selectedProject;
  }

  function renderProjectPolicy() {
    const project = selectedProjectView();
    if (!project) {
      byID("queue-final-review").textContent = "—";
      return;
    }
    const detail = project.policy_version ? ` · v${project.policy_version}` : "";
    byID("queue-final-review").textContent = `${finalReviewLabel(project.final_review)}${detail}`;
  }

  function submissionLabel(status) {
    return { RECEIVED: "待处理", ACCEPTED: "已登记", REJECTED: "已拒绝", UNKNOWN: "结果未知" }[status] || status || "未知";
  }

  function appendActivityRecord(container, title, description, meta, status) {
    const record = document.createElement("article");
    record.className = "activity-record";
    const main = document.createElement("div");
    const heading = document.createElement("h3");
    heading.textContent = title;
    const body = document.createElement("p");
    body.textContent = description;
    main.append(heading, body);
    const side = document.createElement("div");
    side.className = "activity-record-meta";
    side.textContent = `${status}\n${meta}`;
    record.append(main, side);
    container.appendChild(record);
  }

  function renderActivity() {
    if (!overview) return;
    const submissions = overview.submissions || [];
    const list = byID("submission-list");
    clearNode(list);
    byID("submission-empty").hidden = submissions.length !== 0;
    for (const submission of submissions) {
      const title = `${submission.source_client_id || "未知客户端"} · ${submission.task_id || submission.kind}`;
      const description = submission.reason || (submission.status === "ACCEPTED" ? `已写入事件：${(submission.applied_event_ids || []).join(", ") || "无"}` : "候选已接收，等待检查。");
      appendActivityRecord(list, title, description, formatTime(submission.updated_at || submission.received_at), submissionLabel(submission.status));
    }

    const events = byID("activity-event-list");
    clearNode(events);
    const hasTask = Boolean(currentTask);
    byID("activity-open-task").disabled = !hasTask;
    byID("activity-events-empty").hidden = hasTask && currentTask.events.length !== 0;
    byID("activity-task-note").textContent = hasTask ? `${currentTask.task.id} · ${currentTask.task.title}` : "选择一个任务后查看其追加式事件账本。";
    if (hasTask) renderEventsInto(events, currentTask.events);
  }

  function renderHandoffHistory() {
    const list = byID("handoff-history-list");
    clearNode(list);
    const hasTask = Boolean(currentTask);
    const handoffs = hasTask ? (currentTask.handoffs || []) : [];
    byID("prepare-selected-handoff").disabled = !hasTask || currentTask.health !== "HEALTHY";
    byID("handoff-history-empty").hidden = hasTask && handoffs.length !== 0;
    byID("handoff-task-note").textContent = hasTask
      ? `${currentTask.task.id} · ${currentTask.task.title}。系统会根据当前责任方、唯一可用 Binding 和上次同一目标的游标生成下一份包。`
      : "先从待我审查或协作活动中选择一个任务。";
    if (!hasTask) return;
    for (const entry of handoffs) {
      const description = entry.valid
        ? `${entry.adapter} · event #${entry.through_event}\n${entry.output_dir}`
        : `完整性校验失败：${entry.reason || "原因未知"}`;
      appendActivityRecord(list, `→ ${clientName(entry.target_client)}`, description, formatTime(entry.created_at), entry.valid ? "有效" : "异常");
    }
  }

  function renderDeliveries() {
    const deliveries = (overview && overview.deliveries) || [];
    const list = byID("queue-deliveries");
    clearNode(list);
    if (!deliveries.length) {
      const item = document.createElement("li");
      item.className = "table-muted";
      item.textContent = "暂无桌面投递记录";
      list.appendChild(item);
      return;
    }
    for (const delivery of deliveries.slice(0, 6)) {
      const item = document.createElement("li");
      const title = document.createElement("div");
      title.className = "task-title";
      title.textContent = `${delivery.task_id} → ${clientName(delivery.client) || delivery.client}`;
      const meta = document.createElement("div");
      meta.className = "table-muted";
      meta.textContent = `${delivery.status} · ${formatTime(delivery.updated_at)}`;
      item.append(title, meta);
      list.appendChild(item);
    }
  }

  function renderOverview(value, options = {}) {
    overview = value;
    const initialized = Boolean(value.initialized);
    byID("storage-state").textContent = initialized ? "协作存储已初始化" : "需要初始化";
    byID("queue-storage-health").textContent = initialized ? "已初始化" : "尚未初始化";
    byID("queue-client-summary").textContent = `${value.clients.length} 个已登记客户端`;
    renderProjectSelector();
    renderProjectPolicy();
    byID("not-initialized").hidden = initialized;
    const scopedTasks = value.tasks.filter((task) => !selectedProject || task.project_id === selectedProject);
    renderCounts(scopedTasks);
    renderQueue();
    renderAttention(scopedTasks);
    renderSetup();
    renderActivity();
    renderHandoffHistory();
    renderDeliveries();
    if (options.announce !== false) {
      if (!initialized) {
        setStatus("协作中枢尚未初始化；请先完成本机初始化。", "");
      } else {
        setStatus(`已读取 ${value.tasks.length} 个任务；数据不会自动轮询。`, "success");
      }
    }
  }

  function clearStateClasses(node) {
    for (const item of [...node.classList]) {
      if (item.startsWith("state-")) node.classList.remove(item);
    }
  }

  function renderStateRail(status) {
    const rail = byID("state-rail");
    const normalIndex = { DRAFT: 0, ASSIGNED: 1, WORKING: 2, REVIEW: 3, REVISION_REQUIRED: 4, DONE: 5 };
    const currentIndex = normalIndex[status];
    rail.setAttribute("aria-label", status === "BLOCKED" ? "任务当前已阻塞" : `任务当前处于 ${STATE_LABELS[status] || status}`);
    rail.querySelectorAll("li").forEach((node, index) => {
      node.classList.remove("is-complete", "is-current", "is-revision");
      if (currentIndex === undefined) return;
      if (index < currentIndex) node.classList.add("is-complete");
      if (index === currentIndex) node.classList.add("is-current");
      if (status === "REVISION_REQUIRED" && index === currentIndex) node.classList.add("is-revision");
    });
  }

  function renderEvidence(target, values) {
    const container = byID(target);
    clearNode(container);
    for (const entry of values) {
      const record = document.createElement("article");
      record.className = "evidence-record";
      const kind = document.createElement("div");
      kind.className = "evidence-kind";
      kind.textContent = entry.kind || "evidence";
      const summary = document.createElement("div");
      summary.className = "evidence-summary";
      summary.textContent = entry.summary || "—";
      const meta = document.createElement("div");
      meta.className = "evidence-meta";
      meta.textContent = `${entry.created_by || "—"}\n${formatTime(entry.created_at)}`;
      record.append(kind, summary, meta);
      if (entry.file_refs && entry.file_refs.length) {
        const refs = document.createElement("div");
        refs.className = "evidence-files";
        refs.textContent = entry.file_refs.join("\n");
        record.appendChild(refs);
      }
      container.appendChild(record);
    }
  }

  function renderEvents(values) {
    const list = byID("event-list");
    renderEventsInto(list, values);
  }

  function renderEventsInto(list, values) {
    clearNode(list);
    for (const event of values) {
      const item = document.createElement("li");
      const at = document.createElement("time");
      at.className = "event-at";
      at.textContent = formatTime(event.at);
      const marker = document.createElement("span");
      marker.className = "event-marker";
      marker.setAttribute("aria-hidden", "true");
      const main = document.createElement("div");
      main.className = "event-main";
      const heading = document.createElement("div");
      heading.className = "event-title";
      const type = document.createElement("span");
      type.textContent = event.type || "event";
      const actor = document.createElement("span");
      actor.className = "event-actor";
      actor.textContent = event.actor || "—";
      heading.append(type, actor);
      main.appendChild(heading);
      if (event.body) {
        const body = document.createElement("p");
        body.className = "event-body";
        body.textContent = event.body;
        main.appendChild(body);
      }
      const extraValues = [];
      if (event.target_client) extraValues.push(`target: ${event.target_client}`);
      if (event.evidence_refs && event.evidence_refs.length) extraValues.push(`evidence: ${event.evidence_refs.join(", ")}`);
      if (event.origin) extraValues.push(`来源: ${event.origin === "agent" ? "代理" : "人工"}`);
      if (extraValues.length) {
        const extra = document.createElement("div");
        extra.className = "event-extra";
        extra.textContent = extraValues.join(" · ");
        main.appendChild(extra);
      }
      item.append(at, marker, main);
      list.appendChild(item);
    }
  }

  function setTaskDefaults(view) {
    document.querySelectorAll("[data-task-field]").forEach((node) => { node.value = view.task.id; });
    document.querySelectorAll("[data-version-field]").forEach((node) => { node.value = view.state.version; });
    document.querySelectorAll("[data-actor-field]").forEach((node) => { node.value = view.action_actor; });
  }

  function renderRoles(view) {
    const roles = byID("task-roles");
    clearNode(roles);
    [["创建者", view.task.creator], ["审查者", view.task.reviewer], ["当前动作身份", view.action_actor || "—"]].forEach(([label, value]) => {
      const row = document.createElement("div");
      const term = document.createElement("dt");
      term.textContent = label;
      const definition = document.createElement("dd");
      definition.textContent = clientName(value);
      row.append(term, definition);
      roles.appendChild(row);
    });
  }

  function openWriteDialog(formID, title) {
    byID("write-dialog-title").textContent = title;
    document.querySelectorAll(".modal-form").forEach((form) => { form.hidden = form.id !== formID; });
    byID("write-dialog").hidden = false;
    const firstControl = byID(formID).querySelector("input:not([type=hidden]), textarea, select");
    if (firstControl) firstControl.focus();
  }

  function closeWriteDialog() {
    byID("write-dialog").hidden = true;
    document.querySelectorAll(".modal-form").forEach((form) => { form.hidden = true; });
  }

  function openStateAction(action) {
    if (!currentTask) return;
    const form = byID("state-action-form");
    form.elements.namedItem("action").value = action;
    byID("state-action-help").textContent = actionDescription(action);
    const feedbackField = byID("feedback-field");
    const isMessage = action === "message";
    feedbackField.hidden = action !== "request_changes" && !isMessage;
    byID("feedback-label").textContent = isMessage ? "补充消息" : "返工反馈";
    const feedbackInput = feedbackField.querySelector("input[name=feedback]");
    feedbackInput.placeholder = isMessage ? "例如：注意目录权限，完成后直接提交" : "";
    openWriteDialog("state-action-form", ACTION_LABELS[action] || "任务操作");
  }

  function renderActions(view) {
    const step = nextStep(view.state);
    const allowed = [...view.allowed_actions].filter((action) => action === "approve" || action === "request_changes" || action === "message").sort((left, right) => ACTION_PRIORITY.indexOf(left) - ACTION_PRIORITY.indexOf(right));
    setText("next-action-title", step.title);
    setText("next-action-description", step.description);
    const binding = byID("binding-status");
    binding.className = `binding-note ${view.binding_available ? "is-available" : "is-unavailable"}`;
    if (view.available_bindings === 1) binding.textContent = "已识别 1 个可用本机 Binding；可自动生成交接包。";
    else if (view.available_bindings > 1) binding.textContent = `发现 ${view.available_bindings} 个可用 Binding；为避免猜测，自动交接已暂停。`;
    else binding.textContent = "未识别可用本机 Binding；任务仍可阅读，但暂不能自动生成交接包。";
    byID("queue-binding-summary").textContent = view.available_bindings === 1 ? "当前任务可自动交接" : "需要检查 Binding";

    const actions = byID("task-actions");
    clearNode(actions);
    if (!allowed.length) {
      const note = document.createElement("p");
      note.className = "form-note";
      note.textContent = view.state.status === "REVIEW" ? "当前任务没有可用的最终审核操作。" : "当前由代理在原生客户端中推进；网页不会代替它写入任务。";
      actions.appendChild(note);
    }
    allowed.forEach((action, index) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = `button ${index === 0 ? "" : "button-secondary"}`.trim();
      button.textContent = ACTION_LABELS[action] || action;
      button.addEventListener("click", () => openStateAction(action));
      actions.appendChild(button);
    });
    const handoffButton = document.createElement("button");
    handoffButton.type = "button";
    handoffButton.className = "button button-secondary";
    handoffButton.disabled = view.health !== "HEALTHY" || view.available_bindings !== 1;
    handoffButton.textContent = "生成下一份交接包";
    handoffButton.addEventListener("click", prepareNextHandoff);
    actions.appendChild(handoffButton);
  }

  function renderEvidenceExplorer() {
    const empty = byID("evidence-explorer-empty");
    const open = byID("open-selected-task");
    if (!currentTask) {
      empty.hidden = false;
      clearNode(byID("evidence-explorer"));
      open.disabled = true;
      return;
    }
    empty.hidden = currentTask.evidence.length !== 0;
    renderEvidence("evidence-explorer", currentTask.evidence);
    open.disabled = false;
  }

  function renderTask(view) {
    currentTask = view;
    setText("task-id", view.task.id, "TASK");
    setText("task-heading", view.task.title, view.task.id || "任务诊断");
    setText("task-project-context", projectName(view.task.project_id));
    setText("task-subtitle", `项目：${projectName(view.task.project_id)} · 创建：${formatTime(view.task.created_at)}`);
    const taskStatus = byID("task-status");
    clearStateClasses(taskStatus);
    taskStatus.className = `status-label ${statusClass(view.state.status)}`;
    taskStatus.textContent = STATE_LABELS[view.state.status] || view.state.status || "—";
    renderStateRail(view.state.status);
    setText("task-version", `v${view.state.version}`);
    setText("task-responsible", clientName(view.state.responsible_client));
    setText("task-assigned", clientName(view.state.assigned_client));
    const health = byID("task-health");
    health.className = healthClass(view.health);
    health.textContent = view.health || "UNKNOWN";
    setText("task-event", `#${view.state.last_event_id}`);
    setText("task-updated", formatTime(view.state.updated_at));
    setText("task-final-review", finalReviewLabel(view.project.final_review));
    setText("task-worktree", view.worktree ? `${view.worktree.worktree}（认领：${clientName(view.worktree.claimed_by) || view.worktree.claimed_by}）` : "未认领");
    setText("task-objective", view.task.objective, "未提供目标。");

    const acceptance = byID("acceptance-list");
    clearNode(acceptance);
    for (const item of view.task.acceptance || []) {
      const line = document.createElement("li");
      line.textContent = item;
      acceptance.appendChild(line);
    }
    if (!view.task.acceptance || !view.task.acceptance.length) {
      const line = document.createElement("li");
      line.textContent = "未提供验收标准。";
      acceptance.appendChild(line);
    }

    renderEvidence("evidence-list", view.evidence);
    byID("evidence-empty").hidden = view.evidence.length !== 0;
    renderEvents(view.events);
    byID("event-empty").hidden = view.events.length !== 0;
    renderActions(view);
    renderRoles(view);
    setTaskDefaults(view);
    renderEvidenceExplorer();
    renderActivity();
    renderHandoffHistory();
  }

  function showView(name) {
    const targetID = `${name}-view`;
    const target = byID(targetID);
    if (!target) return;
    document.querySelectorAll(".view").forEach((view) => { view.hidden = view.id !== targetID; });
    const activeTarget = name === "task" ? "queue" : name;
    document.querySelectorAll("[data-view-target]").forEach((button) => {
      const active = button.dataset.viewTarget === activeTarget;
      button.classList.toggle("is-active", active);
      if (active) button.setAttribute("aria-current", "page");
      else button.removeAttribute("aria-current");
    });
    if (name === "evidence") renderEvidenceExplorer();
    if (name === "activity") renderActivity();
    if (name === "handoff") renderHandoffHistory();
    window.scrollTo(0, 0);
  }

  async function refreshOverview(options = {}) {
    try {
      renderOverview(await request("GET", `/api/v1/overview${currentQuery()}`), options);
    } catch (error) {
      setStatus(error.message, "error");
      showResult({ error: error.message });
    }
  }

  async function loadTask(taskID, options = {}) {
    try {
      renderTask(await request("GET", `/api/v1/tasks/${encodeURIComponent(taskID)}${currentQuery()}`));
      showView("task");
      if (options.announce !== false) setStatus(`已打开任务 ${taskID}。`, "success");
    } catch (error) {
      setStatus(error.message, "error");
      showResult({ error: error.message });
    }
  }

  function confirmationSummary(title, linesForSummary) {
    return `${title}\n\n${linesForSummary.filter(Boolean).join("\n")}\n\n此操作将写入协作中枢。系统不会自动重试。`;
  }

  function requestConfirmation(title, summary, callback) {
    pendingConfirmation = callback;
    byID("confirm-heading").textContent = title;
    byID("confirm-summary").textContent = summary;
    byID("confirm-dialog").hidden = false;
    byID("accept-confirm").focus();
  }

  function closeConfirmation() {
    pendingConfirmation = null;
    byID("confirm-dialog").hidden = true;
  }

  async function performWrite(path, payload, options = {}) {
    try {
      const response = await request("POST", path, payload);
      showResult(response);
      if (!response.ok) {
        setStatus("写入未成功；请检查结果并在刷新后重新决定下一步。", "error");
        return;
      }
      await refreshOverview({ announce: false });
      if (options.refreshTask && currentTask) await loadTask(currentTask.task.id, { announce: false });
      setStatus(options.successMessage || "写入成功。", "success");
    } catch (error) {
      setStatus(error.message, "error");
      showResult({ error: error.message });
    }
  }

  function confirmWrite(title, summary, path, payload, options) {
    requestConfirmation(title, summary, async () => {
      closeConfirmation();
      await performWrite(path, payload, options);
    });
  }

  function prepareNextHandoff() {
    if (!currentTask) return;
    if (selectedProject && currentTask.task.project_id !== selectedProject) {
      setStatus("当前任务不属于已选择项目，请重新打开任务。", "error");
      return;
    }
    const task = currentTask.task;
    confirmWrite("生成下一份交接包", "生成交接包\n\n系统将自动推断目标客户端、唯一可用 Binding 和事件游标。\n会写入本机交接包与交接历史，但不会改变任务状态，也不会发送或控制客户端。", `/api/v1/tasks/${encodeURIComponent(task.id)}/handoff-next`, {}, { refreshTask: true, successMessage: "交接包已生成；请在交接历史中查看。" });
  }

  function handleStateAction(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const action = field(form, "action");
    const task = field(form, "task");
    const actor = field(form, "actor");
    const feedback = field(form, "feedback");
    const version = Number(field(form, "expectedVersion"));
    if (!currentTask || (selectedProject && currentTask.task.project_id !== selectedProject)) {
      closeWriteDialog();
      setStatus("项目上下文已经变化，请重新打开任务后再审核。", "error");
      return;
    }
    const detail = [`任务：${task}`, `操作身份：${clientName(actor)}`, `expected version：${version}`];
    if (action === "request_changes" || action === "message") detail.push(`内容：${feedback || "（未填写）"}`);
    closeWriteDialog();
    confirmWrite(ACTION_LABELS[action] || "任务操作", confirmationSummary(ACTION_LABELS[action] || action, detail), `/api/v1/tasks/${encodeURIComponent(task)}/actions/${encodeURIComponent(action)}`, {
      actor, feedback, expected_version: version,
    }, { refreshTask: true });
  }

  function bindForms() {
    byID("initialize").addEventListener("click", () => {
      confirmWrite("初始化协作中枢", confirmationSummary("初始化协作中枢", ["范围：当前工作目录", "不会连接、读取或控制任何客户端。"]), "/api/v1/init", {}, { refreshTask: false });
    });
    byID("local-project-form").addEventListener("submit", (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const path = field(form, "path");
      const name = field(form, "name");
      confirmWrite("添加本机项目", confirmationSummary("添加本机项目", [`目录：${path}`, `名称：${name || "自动使用文件夹名称"}`, "项目 ID、设备和 Binding 将自动处理。"]), "/api/v1/local-projects", { path, name }, { refreshTask: false, successMessage: "项目已添加，可以从顶部项目选择器切换。" });
    });
    byID("response-form").addEventListener("submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      try {
        const response = await request("POST", "/api/v1/response-validations", { package: field(form, "package"), input: field(form, "input") });
        showResult(response);
        setStatus(response.ok ? "候选响应已完成只读校验；没有写入任务。" : "候选响应未通过校验；没有写入任务。", response.ok ? "success" : "error");
      } catch (error) {
        setStatus(error.message, "error");
        showResult({ error: error.message });
      }
    });
    byID("state-action-form").addEventListener("submit", handleStateAction);
  }

  function bindNavigation() {
    byID("refresh").addEventListener("click", refreshOverview);
    byID("back-to-queue").addEventListener("click", () => showView("queue"));
    byID("open-selected-task").addEventListener("click", () => {
      if (currentTask) showView("task");
    });
    byID("activity-open-task").addEventListener("click", () => {
      if (currentTask) showView("task");
    });
    byID("prepare-selected-handoff").addEventListener("click", prepareNextHandoff);
    byID("clear-output").addEventListener("click", clearResult);
    byID("close-write-dialog").addEventListener("click", closeWriteDialog);
    byID("cancel-confirm").addEventListener("click", closeConfirmation);
    byID("accept-confirm").addEventListener("click", async () => {
      const callback = pendingConfirmation;
      if (callback) await callback();
    });
    byID("task-search").addEventListener("input", renderQueue);
    byID("project-select").addEventListener("change", (event) => {
      selectedProject = event.currentTarget.value;
      if (currentTask && selectedProject && currentTask.task.project_id !== selectedProject) {
        currentTask = null;
        renderActivity();
        renderHandoffHistory();
        renderEvidenceExplorer();
        showView("queue");
      }
      renderCounts(overview.tasks.filter((task) => !selectedProject || task.project_id === selectedProject));
      renderProjectPolicy();
      renderQueue();
      renderAttention(overview.tasks.filter((task) => !selectedProject || task.project_id === selectedProject));
    });
    document.querySelectorAll("[data-status-filter]").forEach((button) => {
      button.addEventListener("click", () => {
        activeFilter = button.dataset.statusFilter;
        document.querySelectorAll("[data-status-filter]").forEach((node) => node.classList.toggle("is-active", node === button));
        renderQueue();
      });
    });
    document.querySelectorAll("[data-view-target]").forEach((button) => {
      button.addEventListener("click", () => showView(button.dataset.viewTarget));
    });
    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      if (!byID("confirm-dialog").hidden) closeConfirmation();
      else if (!byID("write-dialog").hidden) closeWriteDialog();
    });
  }

  async function start() {
    try {
      const session = await request("GET", "/api/v1/session");
      csrfToken = session.csrf_token;
      bindNavigation();
      bindForms();
      await refreshOverview();
    } catch (error) {
      setStatus(error.message, "error");
      showResult({ error: error.message });
    }
  }

  start();
})();
