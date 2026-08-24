const $ = (id) => document.getElementById(id);
let tunnelID = "";
let cloudflaredAvailable = false;
let tunnelRunning = false;
let actionsBusy = false;

function routeRow(
  route = {
    hostname: "",
    enabled: true,
  },
) {
  const row = document.createElement("div");
  row.className = "route";
  row.innerHTML = `<div class="route-control">
      <span class="route-field-label">Status</span>
      <label class="route-switch">
        <input class="enable" type="checkbox" ${route.enabled ? "checked" : ""} aria-label="Enable public hostname">
        <span class="state-on">Enabled</span>
        <span class="state-off">Disabled</span>
      </label>
    </div>
    <label class="route-hostname"><span class="route-field-label">Hostname</span>
      <input class="hostname" value="${escapeHtml(route.hostname || "")}" placeholder="app.example.com">
    </label>
    <button class="remove" type="button" aria-label="Remove public hostname"><span aria-hidden="true">×</span> Remove</button>`;
  row.querySelector(".remove").onclick = () => row.remove();
  return row;
}

function escapeHtml(s) {
  return String(s).replace(
    /[&<>'"]/g,
    (c) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        "'": "&#39;",
        '"': "&quot;",
      })[c],
  );
}

function showSuccess(text) {
  const message = $("message");
  message.className = "message success";
  message.setAttribute("role", "status");
  $("messageTitle").textContent = "Success";
  $("messageText").textContent = text;
  $("messageDetails").textContent = "";
  $("messageDetails").hidden = true;
  message.hidden = false;
}

function showError(error) {
  const message = $("message");
  const details = [];
  if (error?.stack) details.push(error.stack);
  if (error?.response !== undefined) {
    const status = error.status ? `HTTP ${error.status}` : "API response";
    details.push(`${status}:\n${JSON.stringify(error.response, null, 2)}`);
  }

  message.className = "message error";
  message.setAttribute("role", "alert");
  $("messageTitle").textContent = "Something went wrong";
  $("messageText").textContent = error?.message || String(error);
  $("messageDetails").textContent = details.join("\n\n");
  $("messageDetails").hidden = details.length === 0;
  message.hidden = false;
}

function updateButtons() {
  document.querySelectorAll("button").forEach((button) => {
    button.disabled = actionsBusy;
  });
  if (actionsBusy) return;

  const tunnelToggle = $("tunnelToggle");
  tunnelToggle.textContent = tunnelRunning ? "Stop tunnel" : "Start tunnel";
  tunnelToggle.classList.toggle("good", !tunnelRunning);
  tunnelToggle.classList.toggle("danger", tunnelRunning);
  tunnelToggle.title = tunnelRunning ? "Stop the running tunnel" : "Requires cloudflared";
  tunnelToggle.disabled = !tunnelRunning && !cloudflaredAvailable;
}

function busy(value) {
  actionsBusy = value;
  updateButtons();
}

function collect() {
  return {
    account_id: $("accountID").value.trim(),
    zone_id: $("zoneID").value.trim(),
    api_token: $("apiToken").value.trim(),
    tunnel_id: tunnelID,
    tunnel_name: $("tunnelName").value.trim(),
    cloudflared_path: $("cloudflaredPath").value.trim(),
    origin_service: $("originService").value.trim(),
    match_sni_to_host: $("matchSNI").checked,
    no_tls_verify: $("noTLSVerify").checked,
    auto_start: $("autoStart").checked,
    routes: [...document.querySelectorAll(".route")]
      .map((row) => ({
        hostname: row.querySelector(".hostname").value.trim(),
        enabled: row.querySelector(".enable").checked,
      }))
      .filter((x) => x.hostname),
  };
}

function csrfToken() {
  return document.querySelector('meta[name="zoraxy.csrf.Token"]')?.getAttribute("content") || "";
}

async function api(path, options = {}) {
  const method = String(options.method || "GET").toUpperCase();
  const headers = {
    "Content-Type": "application/json",
    ...options.headers,
  };
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    const token = csrfToken();
    if (token) headers["X-CSRF-Token"] = token;
  }
  const res = await fetch(path, {
    ...options,
    method,
    headers,
  });
  const data = await res.json().catch(() => ({
    ok: false,
    error: `HTTP ${res.status}`,
  }));
  if (!res.ok || data.ok === false) {
    const error = new Error(data.error || `HTTP ${res.status}`);
    error.status = res.status;
    error.response = data;
    throw error;
  }
  return data;
}

async function load() {
  const c = await api("./api/config");
  $("accountID").value = c.account_id || "";
  $("zoneID").value = c.zone_id || "";
  $("apiToken").placeholder = c.token_configured
    ? "Saved token configured — leave blank to keep it"
    : "Cloudflare API token";
  tunnelID = c.tunnel_id || "";
  $("tunnelID").textContent = tunnelID || "not created";
  $("tunnelName").value = c.tunnel_name || "zoraxy";
  $("cloudflaredPath").value = c.cloudflared_path || "cloudflared";
  $("originService").value = c.origin_service || "https://127.0.0.1:443";
  $("matchSNI").checked = c.match_sni_to_host !== false;
  $("noTLSVerify").checked = !!c.no_tls_verify;
  $("autoStart").checked = !!c.auto_start;
  $("routes").replaceChildren(...(c.routes?.length ? c.routes.map(routeRow) : [routeRow()]));
  await refreshStatus();
}

async function save() {
  const cfg = collect();
  await api("./api/config", {
    method: "POST",
    body: JSON.stringify(cfg),
  });
  $("apiToken").value = "";
  showSuccess("Configuration saved.");
}

async function refreshStatus() {
  try {
    const s = await api("./api/status");
    const proc = s.process || {};
    tunnelRunning = !!proc.running;
    const cf = s.cloudflare_status ? ` / Cloudflare: ${s.cloudflare_status}` : "";
    $("statusBadge").textContent = `${proc.running ? `Running PID ${proc.pid}` : "Stopped"}${cf}`;
    $("statusBadge").title = s.cloudflared_error || s.cloudflare_error || "";
    cloudflaredAvailable = !!s.cloudflared_available;
    $("cloudflaredResolved").textContent = s.cloudflared_resolved_path || "not found";
    $("cloudflaredIndicator").classList.toggle("available", cloudflaredAvailable);
    $("cloudflaredAlert").hidden = cloudflaredAvailable;
    updateButtons();
    if (s.tunnel_id) {
      tunnelID = s.tunnel_id;
      $("tunnelID").textContent = tunnelID;
    }
    return s;
  } catch (e) {
    $("statusBadge").textContent = "Status error";
    $("statusBadge").title = e.message;
    throw e;
  }
}

async function action(fn) {
  busy(true);
  try {
    await fn();
    await refreshStatus();
  } catch (e) {
    showError(e);
  } finally {
    busy(false);
    await refreshStatus().catch(() => {});
  }
}

$("addRoute").onclick = () => $("routes").append(routeRow());
$("save").onclick = () => action(save);
$("apply").onclick = () =>
  action(async () => {
    await save();
    const r = await api("./api/apply", {
      method: "POST",
      body: "{}",
    });
    tunnelID = r.tunnel_id || tunnelID;
    $("tunnelID").textContent = tunnelID;
    const count = Array.isArray(r.hostnames) ? r.hostnames.length : 0;
    showSuccess(
      `Cloudflare configuration applied to ${count} public hostname${count === 1 ? "" : "s"}.`,
    );
  });
$("tunnelToggle").onclick = () =>
  action(async () => {
    if (tunnelRunning) {
      await api("./api/tunnel/stop", {
        method: "POST",
        body: "{}",
      });
      showSuccess("Tunnel stopped.");
      return;
    }

    await save();
    const result = await api("./api/tunnel/start", {
      method: "POST",
      body: "{}",
    });
    const pid = result.process?.pid ? ` Process ID: ${result.process.pid}.` : "";
    showSuccess(`Tunnel started.${pid}`);
  });

load().catch(showError);
setInterval(() => refreshStatus().catch(() => {}), 10000);
