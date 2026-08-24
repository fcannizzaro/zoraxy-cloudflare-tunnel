const $ = (id) => document.getElementById(id);
let tunnelID = "";
let cloudflaredAvailable = false;
let tunnelRunning = false;
let actionsBusy = false;
const pendingDNSDeletes = new Set();
let hostnameSuggestions = [];
let pendingRemoveRow = null;

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
      <select class="hostname" aria-label="Public hostname"></select>
    </label>
    <button class="remove" type="button" aria-label="Remove public hostname"><span aria-hidden="true">×</span> Remove</button>`;
  fillHostnameSelect(row.querySelector(".hostname"), route.hostname || "");
  row.querySelector(".remove").onclick = () => removeRoute(row);
  return row;
}

function fillHostnameSelect(select, selectedValue = "") {
  const selected = normalizedHostname(selectedValue);
  const values = [...hostnameSuggestions];
  if (selected && !values.includes(selected)) values.unshift(selected);

  const placeholder = document.createElement("option");
  placeholder.value = "";
  placeholder.textContent = values.length
    ? "Select a Zoraxy hostname…"
    : "No Zoraxy hostnames available";
  placeholder.disabled = values.length > 0;
  placeholder.selected = !selected;

  const options = values.map((hostname) => {
    const option = document.createElement("option");
    option.value = hostname;
    option.textContent = hostname;
    option.selected = hostname === selected;
    return option;
  });
  select.replaceChildren(placeholder, ...options);
}

function normalizedHostname(value) {
  return String(value || "")
    .trim()
    .replace(/\.$/, "")
    .toLowerCase();
}

function updatePendingDNSDeletes() {
  const notice = $("pendingDNSDeletes");
  const count = pendingDNSDeletes.size;
  notice.hidden = count === 0;
  notice.textContent = count
    ? `${count} Cloudflare CNAME record${count === 1 ? " is" : "s are"} queued for deletion on the next Apply.`
    : "";
}

function removeRoute(row) {
  const hostname = normalizedHostname(row.querySelector(".hostname").value);
  if (!hostname) {
    row.remove();
    return;
  }

  pendingRemoveRow = row;
  $("removeDialogHostname").textContent = hostname;
  $("removeHostnameDialog").showModal();
}

function finishRouteRemoval(deleteCNAME) {
  if (!pendingRemoveRow) return;
  const hostname = normalizedHostname(pendingRemoveRow.querySelector(".hostname").value);
  if (deleteCNAME && hostname) pendingDNSDeletes.add(hostname);
  pendingRemoveRow.remove();
  pendingRemoveRow = null;
  $("removeHostnameDialog").close();
  updatePendingDNSDeletes();
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
  pendingDNSDeletes.clear();
  updatePendingDNSDeletes();
  await Promise.all([refreshStatus(), loadHostnameSuggestions()]);
}

async function loadHostnameSuggestions() {
  try {
    const result = await api("./api/zoraxy/hostnames");
    const hostnames = Array.isArray(result.hostnames) ? result.hostnames : [];
    hostnameSuggestions = hostnames;
    document
      .querySelectorAll(".route .hostname")
      .forEach((select) => fillHostnameSelect(select, select.value));
  } catch {
    hostnameSuggestions = [];
  }
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
$("cancelRemoveHostname").onclick = () => {
  pendingRemoveRow = null;
  $("removeHostnameDialog").close();
};
$("keepCNAME").onclick = () => finishRouteRemoval(false);
$("removeCNAME").onclick = () => finishRouteRemoval(true);
$("removeHostnameDialog").addEventListener("cancel", () => {
  pendingRemoveRow = null;
});
$("save").onclick = () => action(save);
$("apply").onclick = () =>
  action(async () => {
    await save();
    const configuredHostnames = new Set(
      collect().routes.map((route) => normalizedHostname(route.hostname)),
    );
    const deleteDNS = [...pendingDNSDeletes].filter(
      (hostname) => !configuredHostnames.has(hostname),
    );
    const r = await api("./api/apply", {
      method: "POST",
      body: JSON.stringify({ delete_dns: deleteDNS }),
    });
    pendingDNSDeletes.clear();
    updatePendingDNSDeletes();
    tunnelID = r.tunnel_id || tunnelID;
    $("tunnelID").textContent = tunnelID;
    const count = Array.isArray(r.hostnames) ? r.hostnames.length : 0;
    const deletedCount = Array.isArray(r.deleted_dns) ? r.deleted_dns.length : 0;
    const deletionSummary = deletedCount
      ? ` Removed ${deletedCount} CNAME record${deletedCount === 1 ? "" : "s"}.`
      : "";
    showSuccess(
      `Cloudflare configuration applied to ${count} public hostname${count === 1 ? "" : "s"}.${deletionSummary}`,
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
