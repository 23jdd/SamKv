const state = {
  apiBase: localStorage.getItem("samkv.apiBase") || "/api",
  logLabels: {},
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
  toast: $("#toast"),
  metricsRaw: $("#metricsRaw"),
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

function renderJSON(target, payload) {
  target.className = "";
  target.innerHTML = `<pre class="json-card">${escapeHTML(JSON.stringify(payload, null, 2))}</pre>`;
}

function renderKVRecords(records) {
  if (!records.length) {
    elements.kvResult.className = "empty-state";
    elements.kvResult.textContent = "没有扫描到记录";
    return;
  }
  elements.kvResult.className = "";
  elements.kvResult.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Key</th><th>Value</th></tr></thead>
        <tbody>
          ${records
            .map(
              (record) => `
                <tr>
                  <td><code>${escapeHTML(record.key)}</code></td>
                  <td>${escapeHTML(record.value)}</td>
                </tr>`,
            )
            .join("")}
        </tbody>
      </table>
    </div>`;
}

function renderLogs(payload) {
  const entries = payload.entries || [];
  if (!entries.length) {
    elements.logResult.className = "empty-state";
    elements.logResult.textContent = "没有匹配的日志";
    return;
  }
  elements.logResult.className = "";
  elements.logResult.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead>
          <tr><th>时间</th><th>Sequence</th><th>Labels</th><th>Message</th></tr>
        </thead>
        <tbody>
          ${entries
            .map(
              (entry) => `
                <tr>
                  <td>${escapeHTML(formatTime(entry.timestamp))}</td>
                  <td>${escapeHTML(String(entry.sequence))}</td>
                  <td>${renderLabels(entry.labels || {})}</td>
                  <td>${escapeHTML(entry.message)}</td>
                </tr>`,
            )
            .join("")}
        </tbody>
      </table>
    </div>
    <p class="muted-line">窗口：${escapeHTML(formatTime(payload.start))} - ${escapeHTML(formatTime(payload.end))}${payload.truncated ? "，结果已截断" : ""}</p>`;
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
    elements.metricsRaw.textContent = text || "没有指标内容";
    renderMetricSummary();
    renderMetricBars();
  } catch (error) {
    elements.metricsRaw.textContent = error.message;
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

function fillSamples() {
  $("#kvKey").value = "app/config";
  $("#kvValue").value = "enabled";
  $("#scanStart").value = "app/";
  $("#scanEnd").value = "app/z";
}

function fillLogSample() {
  setLogLabels({ app: "api", level: "ERROR", host: "node-1" });
  $("#logMessage").value = "request failed";
  $("#logQuery").value = '"failed"{app=api,level=ERROR}[1h]';
  $("#logLimit").value = "100";
  $("#logBatchJSON").value = JSON.stringify(
    {
      entries: [
        { labels: { app: "api", level: "INFO" }, message: "request started" },
        { labels: { app: "api", level: "ERROR" }, message: "request failed" },
      ],
    },
    null,
    2,
  );
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
  $("#sampleKV").addEventListener("click", fillSamples);
  $("#sampleLog").addEventListener("click", fillLogSample);
  $("#addLogLabel").addEventListener("click", () => {
    try {
      addLogLabel();
    } catch (error) {
      showToast(error.message, "error");
    }
  });
  $("#labelName").addEventListener("keydown", handleLabelInputKeydown);
  $("#labelValue").addEventListener("keydown", handleLabelInputKeydown);
  elements.logLabelList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-label]");
    if (!button) return;
    delete state.logLabels[button.dataset.label];
    renderLabelEditor();
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
      renderJSON(elements.kvResult, { ok: true, action: "put", key });
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
      renderJSON(elements.kvResult, payload);
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
      renderJSON(elements.kvResult, { ok: true, action: "delete", key });
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
      renderKVRecords(payload.records || []);
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
      renderJSON(elements.logResult, payload);
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
      renderJSON(elements.logResult, payload);
      showToast(`批量写入成功：${payload.sequences.length} 条`);
      await loadMetrics();
    } catch (error) {
      renderError(elements.logResult, error);
    }
  });

  $("#logQueryForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const params = new URLSearchParams();
    params.set("query", requiredValue("#logQuery", "QueryFormat 不能为空"));
    params.set("limit", $("#logLimit").value || "100");
    try {
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

function requiredValue(selector, message) {
  const value = $(selector).value.trim();
  if (!value) throw new Error(message);
  return value;
}

function renderError(target, error) {
  target.className = "";
  target.innerHTML = `<pre class="json-card">${escapeHTML(error.message)}</pre>`;
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
fillSamples();
fillLogSample();
refreshAll();
