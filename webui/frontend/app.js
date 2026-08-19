const state = {
  apiBase: localStorage.getItem("samkv.apiBase") || "/api",
  logLabels: {},
  queryLabels: {},
  metrics: {},
  rawMetrics: "",
};

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

const elements = {
  apiBase: $("#apiBase"),
  connectionText: $("#connectionText"),
  healthDot: $("#healthDot"),
  healthTitle: $("#healthTitle"),
  healthDetail: $("#healthDetail"),
  kvResult: $("#kvResult"),
  logResult: $("#logResult"),
  logLabelList: $("#logLabelList"),
  queryLabelList: $("#queryLabelList"),
  toast: $("#toast"),
  metricBars: $("#metricBars"),
};

function setBusy(button, busy) {
  if (!button) return;
  button.disabled = busy;
}

function showToast(message, type = "info") {
  elements.toast.textContent = message;
  elements.toast.className = `toast show ${type === "error" ? "error" : ""}`;
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => {
    elements.toast.className = "toast";
  }, 2600);
}

function apiURL(path) {
  return `${state.apiBase.replace(/\/$/, "")}${path}`;
}

async function request(path, options = {}) {
  const response = await fetch(apiURL(path), {
    headers: {
      Accept: "application/json",
      ...(options.body ? { "Content-Type": "application/json" } : {}),
      ...options.headers,
    },
    ...options,
  });
  const text = await response.text();
  const contentType = response.headers.get("content-type") || "";
  const payload = contentType.includes("application/json") && text ? JSON.parse(text) : text;

  if (!response.ok) {
    const message = payload && payload.error ? payload.error : `HTTP ${response.status}`;
    throw new Error(message);
  }
  return payload;
}

function renderKVActionResult(action, detail) {
  const actionText = {
    put: "写入成功",
    get: "读取成功",
    delete: "删除成功",
  }[action];
  const value = detail.value ?? "";
  const showValue = action !== "delete";
  elements.kvResult.className = "";
  elements.kvResult.innerHTML = `
    <div class="result-shell">
      <div class="result-summary ${action === "delete" ? "danger-summary" : ""}">
        <div>
          <span class="result-badge">${escapeHTML(actionText)}</span>
          <h4>${escapeHTML(detail.key)}</h4>
        </div>
        <div class="summary-stats">
          <span><strong>${escapeHTML(action.toUpperCase())}</strong> 操作</span>
          <span><strong>${formatBytes(byteLength(value))}</strong> Value</span>
          <span><strong>${escapeHTML(new Date().toLocaleTimeString("zh-CN", { hour12: false }))}</strong> 完成</span>
        </div>
      </div>
      <div class="detail-grid">
        <div class="detail-item">
          <span>Key</span>
          <code>${escapeHTML(detail.key)}</code>
        </div>
        ${
          showValue
            ? `<div class="detail-item wide">
                <span>Value</span>
                <pre>${escapeHTML(value)}</pre>
              </div>`
            : `<div class="detail-item wide">
                <span>Status</span>
                <p>该 key 已提交删除请求。</p>
              </div>`
        }
      </div>
      ${
        showValue
          ? `<div class="result-actions">
              <button type="button" class="secondary" data-copy="${escapeHTML(value)}">复制 Value</button>
              <button type="button" class="ghost" data-copy="${escapeHTML(detail.key)}">复制 Key</button>
            </div>`
          : `<div class="result-actions">
              <button type="button" class="ghost" data-copy="${escapeHTML(detail.key)}">复制 Key</button>
            </div>`
      }
    </div>`;
}

function renderKVRecords(records, context = {}) {
  if (!records.length) {
    renderEmptyResult(elements.kvResult, "没有扫描到记录", "调整 Start / End 范围后再试一次。");
    return;
  }
  const totalValueBytes = records.reduce((sum, record) => sum + byteLength(record.value), 0);
  elements.kvResult.className = "";
  elements.kvResult.innerHTML = `
    <div class="result-shell">
      <div class="result-summary">
        <div>
          <span class="result-badge">扫描完成</span>
          <h4>${formatNumber(records.length)} 条记录</h4>
        </div>
        <div class="summary-stats">
          <span><strong>${escapeHTML(context.start || "最小 key")}</strong> Start</span>
          <span><strong>${escapeHTML(context.end || "最大 key")}</strong> End</span>
          <span><strong>${formatBytes(totalValueBytes)}</strong> Value</span>
        </div>
      </div>
      <div class="record-list">
        ${records
          .map(
            (record) => `
              <article class="kv-record">
                <div class="kv-record-main">
                  <code>${escapeHTML(record.key)}</code>
                  <p>${escapeHTML(record.value)}</p>
                </div>
                <div class="record-meta">
                  <span>${formatBytes(byteLength(record.value))}</span>
                  <button type="button" class="ghost" data-copy="${escapeHTML(record.value)}">复制</button>
                </div>
              </article>`,
          )
          .join("")}
      </div>
    </div>`;
}

function renderLogs(payload) {
  const entries = payload.entries || [];
  if (!entries.length) {
    renderEmptyResult(elements.logResult, "没有匹配的日志", "放宽 Search、Range 或 Filter Labels 后再查询。");
    return;
  }
  elements.logResult.className = "";
  elements.logResult.innerHTML = `
    <div class="result-shell">
      <div class="result-summary">
        <div>
          <span class="result-badge">${payload.truncated ? "结果已截断" : "查询完成"}</span>
          <h4>${formatNumber(entries.length)} 条日志</h4>
        </div>
        <div class="summary-stats">
          <span><strong>${escapeHTML(payload.matcher || "*")}</strong> Matcher</span>
          <span><strong>${escapeHTML(formatTime(payload.start))}</strong> Start</span>
          <span><strong>${escapeHTML(formatTime(payload.end))}</strong> End</span>
        </div>
      </div>
      <div class="log-stream">
        ${entries
          .map(
            (entry) => `
              <article class="log-entry">
                <div class="log-entry-head">
                  <div>
                    <time>${escapeHTML(formatTime(entry.timestamp))}</time>
                    <strong>#${escapeHTML(String(entry.sequence))}</strong>
                  </div>
                  <button type="button" class="ghost" data-copy="${escapeHTML(entry.message)}">复制 Message</button>
                </div>
                <div class="log-labels">${renderLabels(entry.labels || {})}</div>
                <pre>${escapeHTML(entry.message)}</pre>
              </article>`,
          )
          .join("")}
      </div>
    </div>`;
}

function renderLogWriteResult(payload, requestBody) {
  elements.logResult.className = "";
  elements.logResult.innerHTML = `
    <div class="result-shell">
      <div class="result-summary">
        <div>
          <span class="result-badge">日志写入成功</span>
          <h4>Sequence #${escapeHTML(String(payload.sequence))}</h4>
        </div>
        <div class="summary-stats">
          <span><strong>${formatNumber(Object.keys(requestBody.labels || {}).length)}</strong> Labels</span>
          <span><strong>${formatBytes(byteLength(requestBody.message))}</strong> Message</span>
          <span><strong>${escapeHTML(requestBody.timestamp ? formatTime(requestBody.timestamp) : "服务端时间")}</strong> Timestamp</span>
        </div>
      </div>
      <div class="log-entry single">
        <div class="log-labels">${renderLabels(requestBody.labels || {})}</div>
        <pre>${escapeHTML(requestBody.message)}</pre>
      </div>
    </div>`;
}

function renderLogBatchResult(payload) {
  const sequences = payload.sequences || [];
  elements.logResult.className = "";
  elements.logResult.innerHTML = `
    <div class="result-shell">
      <div class="result-summary">
        <div>
          <span class="result-badge">批量写入成功</span>
          <h4>${formatNumber(sequences.length)} 条日志</h4>
        </div>
        <div class="summary-stats">
          <span><strong>${escapeHTML(String(sequences[0] ?? "--"))}</strong> First</span>
          <span><strong>${escapeHTML(String(sequences.at(-1) ?? "--"))}</strong> Last</span>
        </div>
      </div>
      <div class="sequence-grid">
        ${sequences.map((sequence) => `<code>#${escapeHTML(String(sequence))}</code>`).join("")}
      </div>
    </div>`;
}

function renderLabels(labels) {
  const names = Object.keys(labels).sort();
  if (!names.length) return "<span class=\"pill\">无标签</span>";
  return names
    .map((name) => `<span class="pill">${escapeHTML(name)}=${escapeHTML(labels[name])}</span>`)
    .join("");
}

function renderLabelEditor() {
  const names = Object.keys(state.logLabels).sort();
  elements.logLabelList.innerHTML = names
    .map(
      (name) => `
        <span class="label-chip">
          <span>${escapeHTML(name)}=${escapeHTML(state.logLabels[name])}</span>
          <button type="button" data-label="${escapeHTML(name)}" title="移除 ${escapeHTML(name)}">x</button>
        </span>`,
    )
    .join("");
}

function addLogLabel() {
  const nameInput = $("#labelName");
  const valueInput = $("#labelValue");
  const name = nameInput.value.trim();
  const value = valueInput.value.trim();
  if (!name) {
    throw new Error("Label name 不能为空");
  }
  if (!value) {
    throw new Error("Label value 不能为空");
  }
  state.logLabels[name] = value;
  nameInput.value = "";
  valueInput.value = "";
  nameInput.focus();
  renderLabelEditor();
}

function setLogLabels(labels) {
  state.logLabels = { ...labels };
  renderLabelEditor();
}

function currentLogLabels() {
  return { ...state.logLabels };
}

function renderQueryLabelEditor() {
  const names = Object.keys(state.queryLabels).sort();
  elements.queryLabelList.innerHTML = names
    .map(
      (name) => `
        <span class="label-chip">
          <span>${escapeHTML(name)}=${escapeHTML(state.queryLabels[name])}</span>
          <button type="button" data-label="${escapeHTML(name)}" title="移除 ${escapeHTML(name)}">x</button>
        </span>`,
    )
    .join("");
}

function addQueryLabel() {
  const nameInput = $("#queryLabelName");
  const valueInput = $("#queryLabelValue");
  const name = nameInput.value.trim();
  const value = valueInput.value.trim();
  if (!name) {
    throw new Error("Filter label name 不能为空");
  }
  if (!isValidLabelName(name)) {
    throw new Error("Filter label name 只能包含字母、数字、_、.、:、/、-，且不能以数字开头");
  }
  if (!value) {
    throw new Error("Filter label value 不能为空");
  }
  state.queryLabels[name] = value;
  nameInput.value = "";
  valueInput.value = "";
  nameInput.focus();
  renderQueryLabelEditor();
  updateLogQueryPreview();
}

function buildLogQueryFromBuilder() {
  const matcher = $("#queryMatcher").value.trim();
  if (!matcher) {
    throw new Error("Search 不能为空");
  }
  const labels = Object.keys(state.queryLabels)
    .sort()
    .map((name) => `${name}=${quoteQueryToken(state.queryLabels[name])}`)
    .join(",");
  const range = $("#queryRange").value;
  const offset = $("#queryOffset").value.trim();
  const offsetPart = offset && offset !== "0" ? ` offset ${offset}` : "";
  return `${quoteQueryToken(matcher)}{${labels}}[${range}]${offsetPart}`;
}

function updateLogQueryPreview() {
  if (!$("#queryMatcher").value.trim()) return;
  try {
    $("#logQuery").value = buildLogQueryFromBuilder();
  } catch (_) {
    // The explicit submit path shows validation errors; live preview stays quiet.
  }
}

function clearQueryFilters() {
  $("#queryMatcher").value = "";
  $("#queryRange").value = "1h";
  $("#queryOffset").value = "";
  $("#logLimit").value = "100";
  $("#logQuery").value = "";
  state.queryLabels = {};
  renderQueryLabelEditor();
}

function activeLogQuery() {
  const matcher = $("#queryMatcher").value.trim();
  if (matcher) {
    const query = buildLogQueryFromBuilder();
    $("#logQuery").value = query;
    return query;
  }
  return requiredValue("#logQuery", "QueryFormat 不能为空");
}

function quoteQueryToken(value) {
  return `"${String(value).replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}

function isValidLabelName(value) {
  return /^[\p{L}_][\p{L}\p{N}_.:/-]*$/u.test(value);
}

function optionalTimestamp(value) {
  if (!value) return undefined;
  return new Date(value).toISOString();
}

async function checkHealth() {
  try {
    const payload = await request("/healthz");
    elements.healthDot.className = "health-dot ok";
    elements.healthTitle.textContent = "运行正常";
    elements.healthDetail.textContent = `健康检查返回：${payload.status}`;
  } catch (error) {
    elements.healthDot.className = "health-dot fail";
    elements.healthTitle.textContent = "连接失败";
    elements.healthDetail.textContent = error.message;
  }
}

async function loadMetrics() {
  try {
    const text = await request("/metrics", { headers: { Accept: "text/plain" } });
    state.rawMetrics = text;
    state.metrics = parseMetrics(text);
    renderMetricSummary();
    renderMetricBars();
  } catch (error) {
    renderMetricSummary(true);
    elements.metricBars.innerHTML = `<div class="empty-state">${escapeHTML(error.message)}</div>`;
  }
}

function parseMetrics(text) {
  const metrics = {};
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const match = trimmed.match(/^([^\s]+)\s+(-?\d+(?:\.\d+)?)$/);
    if (match) metrics[match[1]] = Number(match[2]);
  }
  return metrics;
}

function metric(name) {
  return state.metrics[name] ?? 0;
}

function renderMetricSummary(failed = false) {
  if (failed) {
    $("#metricWrites").textContent = "--";
    $("#metricReads").textContent = "--";
    $("#metricSSTables").textContent = "--";
    $("#metricCacheRate").textContent = "--";
    return;
  }
  const hits = metric("samkv_block_cache_hits_total");
  const misses = metric("samkv_block_cache_misses_total");
  const cacheRate = hits + misses === 0 ? "--" : `${Math.round((hits / (hits + misses)) * 100)}%`;
  $("#metricWrites").textContent = formatNumber(metric("samkv_write_operations_total"));
  $("#metricReads").textContent = formatNumber(metric("samkv_read_operations_total"));
  $("#metricSSTables").textContent = formatNumber(metric("samkv_sstables"));
  $("#metricCacheRate").textContent = cacheRate;
}

function renderMetricBars() {
  const items = [
    ["Active MemTable", metric("samkv_active_memtable_bytes"), "bytes"],
    ["Immutable Entries", metric("samkv_immutable_entries"), "number"],
    ["WAL Bytes", metric("samkv_wal_bytes"), "bytes"],
    ["SSTable Bytes", metric("samkv_sstable_bytes"), "bytes"],
    ["Block Cache Bytes", metric("samkv_block_cache_bytes"), "bytes"],
    ["Compactions", metric("samkv_compactions_total"), "number"],
  ];
  const max = Math.max(...items.map(([, value]) => value), 1);
  elements.metricBars.innerHTML = items
    .map(([label, value, type]) => {
      const percent = Math.max(4, Math.round((value / max) * 100));
      const hot = percent > 75 ? "hot" : percent > 45 ? "warn" : "";
      const display = type === "bytes" ? formatBytes(value) : formatNumber(value);
      return `
        <div class="bar-row">
          <div class="bar-meta"><span>${escapeHTML(label)}</span><strong>${escapeHTML(display)}</strong></div>
          <div class="bar-track"><div class="bar-fill ${hot}" style="width:${percent}%"></div></div>
        </div>`;
    })
    .join("");
}

async function refreshAll() {
  await Promise.all([checkHealth(), loadMetrics()]);
}

function bindNavigation() {
  const links = $$(".nav a");
  links.forEach((link) => {
    link.addEventListener("click", () => {
      links.forEach((item) => item.classList.remove("active"));
      link.classList.add("active");
    });
  });
}

function bindForms() {
  elements.apiBase.value = state.apiBase;
  $("#saveApiBase").addEventListener("click", () => {
    state.apiBase = elements.apiBase.value.trim() || "/api";
    localStorage.setItem("samkv.apiBase", state.apiBase);
    elements.connectionText.textContent = `当前 API：${state.apiBase}`;
    showToast("API 地址已保存");
    refreshAll();
  });

  $("#refreshAll").addEventListener("click", refreshAll);
  $("#refreshMetrics").addEventListener("click", loadMetrics);
  $("#addLogLabel").addEventListener("click", () => {
    try {
      addLogLabel();
    } catch (error) {
      showToast(error.message, "error");
    }
  });
  $("#labelName").addEventListener("keydown", handleLabelInputKeydown);
  $("#labelValue").addEventListener("keydown", handleLabelInputKeydown);
  $("#addQueryLabel").addEventListener("click", () => {
    try {
      addQueryLabel();
    } catch (error) {
      showToast(error.message, "error");
    }
  });
  $("#queryLabelName").addEventListener("keydown", handleQueryLabelInputKeydown);
  $("#queryLabelValue").addEventListener("keydown", handleQueryLabelInputKeydown);
  $("#queryMatcher").addEventListener("input", updateLogQueryPreview);
  $("#queryRange").addEventListener("change", updateLogQueryPreview);
  $("#queryOffset").addEventListener("input", updateLogQueryPreview);
  $("#buildLogQuery").addEventListener("click", () => {
    try {
      $("#logQuery").value = buildLogQueryFromBuilder();
      showToast("查询已生成");
    } catch (error) {
      showToast(error.message, "error");
    }
  });
  $("#clearQueryFilters").addEventListener("click", clearQueryFilters);
  elements.kvResult.addEventListener("click", handleResultAction);
  elements.logResult.addEventListener("click", handleResultAction);
  elements.logLabelList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-label]");
    if (!button) return;
    delete state.logLabels[button.dataset.label];
    renderLabelEditor();
  });
  elements.queryLabelList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-label]");
    if (!button) return;
    delete state.queryLabels[button.dataset.label];
    renderQueryLabelEditor();
    updateLogQueryPreview();
  });
  $("#clearKVResult").addEventListener("click", () => {
    elements.kvResult.className = "empty-state";
    elements.kvResult.textContent = "等待操作结果";
  });
  $("#clearLogResult").addEventListener("click", () => {
    elements.logResult.className = "empty-state";
    elements.logResult.textContent = "等待日志操作结果";
  });

  $("#kvForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = $("#putKV");
    setBusy(button, true);
    try {
      const key = requiredValue("#kvKey", "Key 不能为空");
      const value = $("#kvValue").value;
      await request(`/kv/${encodePath(key)}`, {
        method: "PUT",
        body: JSON.stringify({ value }),
      });
      renderKVActionResult("put", { key, value });
      showToast("KV 写入成功");
      await loadMetrics();
    } catch (error) {
      renderError(elements.kvResult, error);
    } finally {
      setBusy(button, false);
    }
  });

  $("#getKV").addEventListener("click", async () => {
    const button = $("#getKV");
    setBusy(button, true);
    try {
      const key = requiredValue("#kvKey", "Key 不能为空");
      const payload = await request(`/kv/${encodePath(key)}`);
      $("#kvValue").value = payload.value;
      renderKVActionResult("get", payload);
      showToast("KV 读取成功");
      await loadMetrics();
    } catch (error) {
      renderError(elements.kvResult, error);
    } finally {
      setBusy(button, false);
    }
  });

  $("#deleteKV").addEventListener("click", async () => {
    const button = $("#deleteKV");
    setBusy(button, true);
    try {
      const key = requiredValue("#kvKey", "Key 不能为空");
      await request(`/kv/${encodePath(key)}`, { method: "DELETE" });
      renderKVActionResult("delete", { key });
      showToast("KV 删除成功");
      await loadMetrics();
    } catch (error) {
      renderError(elements.kvResult, error);
    } finally {
      setBusy(button, false);
    }
  });

  $("#scanForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const params = new URLSearchParams();
    if ($("#scanStart").value) params.set("start", $("#scanStart").value);
    if ($("#scanEnd").value) params.set("end", $("#scanEnd").value);
    try {
      const payload = await request(`/scan?${params.toString()}`);
      renderKVRecords(payload.records || [], {
        start: $("#scanStart").value.trim(),
        end: $("#scanEnd").value.trim(),
      });
      showToast(`扫描完成，共 ${payload.records?.length || 0} 条`);
      await loadMetrics();
    } catch (error) {
      renderError(elements.kvResult, error);
    }
  });

  $("#logWriteForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const body = {
        labels: currentLogLabels(),
        message: requiredValue("#logMessage", "Message 不能为空"),
      };
      const timestamp = optionalTimestamp($("#logTimestamp").value);
      if (timestamp) body.timestamp = timestamp;
      const payload = await request("/logs", {
        method: "POST",
        body: JSON.stringify(body),
      });
      renderLogWriteResult(payload, body);
      showToast(`日志写入成功：${payload.sequence}`);
      await loadMetrics();
    } catch (error) {
      renderError(elements.logResult, error);
    }
  });

  $("#logBatchForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const parsed = JSON.parse($("#logBatchJSON").value);
      const body = Array.isArray(parsed) ? { entries: parsed } : parsed;
      const payload = await request("/logs/batch", {
        method: "POST",
        body: JSON.stringify(body),
      });
      renderLogBatchResult(payload);
      showToast(`批量写入成功：${payload.sequences.length} 条`);
      await loadMetrics();
    } catch (error) {
      renderError(elements.logResult, error);
    }
  });

  $("#logQueryForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const params = new URLSearchParams();
    try {
      params.set("query", activeLogQuery());
      params.set("limit", $("#logLimit").value || "100");
      const payload = await request(`/logs/query?${params.toString()}`);
      renderLogs(payload);
      showToast(`查询完成，共 ${payload.entries?.length || 0} 条`);
      await loadMetrics();
    } catch (error) {
      renderError(elements.logResult, error);
    }
  });
}

function handleLabelInputKeydown(event) {
  if (event.key !== "Enter") return;
  event.preventDefault();
  try {
    addLogLabel();
  } catch (error) {
    showToast(error.message, "error");
  }
}

function handleQueryLabelInputKeydown(event) {
  if (event.key !== "Enter") return;
  event.preventDefault();
  try {
    addQueryLabel();
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function handleResultAction(event) {
  const copyButton = event.target.closest("button[data-copy]");
  if (!copyButton) return;
  try {
    await copyText(copyButton.dataset.copy);
    showToast("已复制");
  } catch (error) {
    showToast(error.message, "error");
  }
}

async function copyText(value) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const ok = document.execCommand("copy");
  textarea.remove();
  if (!ok) {
    throw new Error("复制失败");
  }
}

function requiredValue(selector, message) {
  const value = $(selector).value.trim();
  if (!value) throw new Error(message);
  return value;
}

function renderEmptyResult(target, title, detail) {
  target.className = "empty-state result-empty";
  target.innerHTML = `
    <strong>${escapeHTML(title)}</strong>
    <span>${escapeHTML(detail)}</span>`;
}

function renderError(target, error) {
  target.className = "";
  target.innerHTML = `
    <div class="result-shell">
      <div class="result-summary danger-summary">
        <div>
          <span class="result-badge">操作失败</span>
          <h4>${escapeHTML(error.message)}</h4>
        </div>
      </div>
    </div>`;
  showToast(error.message, "error");
}

function encodePath(path) {
  return path
    .split("/")
    .map((part) => encodeURIComponent(part))
    .join("/");
}

function formatNumber(value) {
  return new Intl.NumberFormat("zh-CN").format(value || 0);
}

function formatBytes(value) {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let size = value || 0;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function byteLength(value) {
  return new TextEncoder().encode(String(value ?? "")).length;
}

function formatTime(value) {
  if (!value) return "";
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

bindNavigation();
bindForms();
refreshAll();
