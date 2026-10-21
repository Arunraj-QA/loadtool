// LoadTool UI (ADR-023). It only configures runs and shows what the
// controller returns: numbers come from the engine's time-series points
// while running, and from the JSON report when the run ends. Nothing is
// computed here except a per-second rate from a point's interval.
"use strict";

const $ = (id) => document.getElementById(id);

// The session token arrives in the URL fragment, which the browser never
// sends; keep it for this tab and remove it from the address bar. A new
// URL pasted into the same tab (after restarting loadtool ui) changes
// only the fragment, so read it again then.
let token = "";
function readToken() {
  const m = location.hash.match(/token=([0-9a-f]+)/);
  try {
    if (m) sessionStorage.setItem("loadtoolToken", m[1]);
    if (location.hash) history.replaceState(null, "", location.pathname);
    token = m ? m[1] : sessionStorage.getItem("loadtoolToken") || "";
  } catch {
    if (m) token = m[1];
  }
}
readToken();
window.addEventListener("hashchange", () => {
  readToken();
  setText("formError", "");
});

async function api(path, body) {
  const opts = { method: body === undefined ? "GET" : "POST", headers: { "X-LoadTool-Token": token } };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const type = res.headers.get("Content-Type") || "";
  const data = type.includes("application/json") ? await res.json() : await res.text();
  if (!res.ok) throw new Error((data && data.error) || `HTTP ${res.status}`);
  return data;
}

// Query parameter and header rows. A secret value is masked here and
// replaced with *** in everything the controller returns.
function addField(listId, name = "", value = "", secret = false) {
  const row = document.createElement("div");
  row.className = "field";
  const n = Object.assign(document.createElement("input"), { placeholder: "name", value: name, autocomplete: "off" });
  const v = Object.assign(document.createElement("input"), { placeholder: "value", value, autocomplete: "off", type: secret ? "password" : "text" });
  n.setAttribute("aria-label", "name");
  v.setAttribute("aria-label", "value");
  const lab = document.createElement("label");
  const s = Object.assign(document.createElement("input"), { type: "checkbox", checked: secret });
  s.addEventListener("change", () => { v.type = s.checked ? "password" : "text"; });
  lab.append(s, "secret");
  const del = Object.assign(document.createElement("button"), { type: "button", className: "icon", textContent: "✕" });
  del.setAttribute("aria-label", "remove");
  del.addEventListener("click", () => row.remove());
  row.append(n, v, lab, del);
  $(listId).append(row);
}

function fields(listId) {
  return [...$(listId).querySelectorAll(".field")]
    .map((r) => { const [n, v, , ] = r.querySelectorAll("input"); return { name: n.value.trim(), value: v.value, secret: r.querySelector("input[type=checkbox]").checked }; })
    .filter((f) => f.name !== "");
}

function requestFromForm() {
  const type = $("authType").value;
  return {
    method: $("method").value,
    url: $("url").value.trim(),
    query: fields("query"),
    headers: fields("headers"),
    body: $("body").value,
    auth: { type, token: type === "bearer" ? $("token").value : "", username: type === "basic" ? $("username").value : "", password: type === "basic" ? $("password").value : "" },
  };
}

function runFromForm() {
  const num = (id) => (($(id).value || "").trim() === "" ? 0 : Number($(id).value));
  return {
    request: requestFromForm(),
    vus: num("vus"),
    duration: $("duration").value.trim(),
    minStatus: num("minStatus"),
    maxStatus: num("maxStatus"),
    p95Ms: num("p95"),
    maxErrorPct: num("errPct"),
    authorized: $("authorized").checked,
  };
}

// Formatting.
const fmtInt = (n) => (n == null ? "–" : Math.round(n).toLocaleString());
const fmtMs = (n) => (n == null ? "–" : n < 10 ? `${n.toFixed(2)} ms` : n < 1000 ? `${n.toFixed(1)} ms` : `${(n / 1000).toFixed(2)} s`);
const fmtPct = (f) => (f == null ? "–" : `${(f * 100).toFixed(2)} %`);
const fmtSecs = (ms) => `${(ms / 1000).toFixed(1)} s`;

function setText(id, text) { $(id).textContent = text; }

// A minimal line chart: values over time, scaled to the box.
function chart(svgId, xs, ys, cls) {
  const svg = $(svgId);
  const W = 400, H = 140, L = 34, B = 18, T = 8, R = 6;
  const max = Math.max(1, ...ys);
  const xmax = Math.max(1, ...xs);
  const px = (x) => L + (x / xmax) * (W - L - R);
  const py = (y) => H - B - (y / max) * (H - B - T);
  const pts = xs.map((x, i) => `${px(x).toFixed(1)},${py(ys[i]).toFixed(1)}`).join(" ");
  svg.innerHTML =
    `<line class="axis" x1="${L}" y1="${H - B}" x2="${W - R}" y2="${H - B}"/>` +
    `<line class="axis" x1="${L}" y1="${T}" x2="${L}" y2="${H - B}"/>` +
    `<text class="tick" x="${L - 4}" y="${T + 8}" text-anchor="end">${fmtShort(max)}</text>` +
    `<text class="tick" x="${L - 4}" y="${H - B}" text-anchor="end">0</text>` +
    `<text class="tick" x="${W - R}" y="${H - 4}" text-anchor="end">${xmax.toFixed(0)} s</text>` +
    (xs.length ? `<polyline class="${cls}" points="${pts}"/>` : "");
}
const fmtShort = (n) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : n >= 10 ? n.toFixed(0) : n.toFixed(1));

let current = null; // the run being shown
let timer = null;

function render(st) {
  const state = st.state;
  const badge = $("state");
  badge.textContent = state;
  badge.className = `badge ${state}`;
  setText("label", st.label);
  setText("elapsed", `${fmtSecs(Math.min(st.elapsedMs, st.durationMs + 60000))} / ${fmtSecs(st.durationMs)}`);
  setText("vusOut", `${state === "running" ? st.activeVUs : "–"} / ${st.configuredVUs}`);
  setText("runError", st.error || "");
  $("warnings").replaceChildren(...(st.warnings || []).map((w) => Object.assign(document.createElement("li"), { textContent: w })));

  // Charts skip points covering under half a second (the run's last
  // fraction of a second): divided by a short interval, their rate would
  // spike. Totals still include them.
  const pts = st.live.points;
  const xs = [], rps = [], p95 = [];
  pts.forEach((p, i) => {
    const secs = (p.atMs - (i ? pts[i - 1].atMs : 0)) / 1000;
    if (secs < 0.5) return;
    xs.push(p.atMs / 1000);
    rps.push(p.requests / secs);
    p95.push(p.p95Ms);
  });
  chart("chartRps", xs, rps, "line1");
  chart("chartP95", xs, p95, "line2");

  if (st.final) {
    showFinal(st.final);
    return;
  }
  // Live: totals so far, and the latest second's rate and latency.
  const last = pts[pts.length - 1];
  setText("requests", fmtInt(st.live.requests));
  setText("failed", fmtInt(st.live.failed));
  setText("errRate", st.live.requests ? fmtPct(st.live.failed / st.live.requests) : "–");
  setText("rps", rps.length ? fmtInt(rps[rps.length - 1]) : "–");
  setText("latHint", "latest second while running; whole run at the end");
  setText("avg", last ? fmtMs(last.meanMs) : "–");
  setText("p50", last ? fmtMs(last.p50Ms) : "–");
  setText("p90", "–");
  setText("p95v", last ? fmtMs(last.p95Ms) : "–");
  setText("p99", last ? fmtMs(last.p99Ms) : "–");
}

// The final numbers are the JSON report's, unchanged.
function showFinal(rep) {
  const m = rep.metrics;
  const d = m.http_req_duration || {};
  setText("requests", fmtInt(m.http_reqs.count));
  setText("rps", fmtInt(m.http_reqs.rate));
  setText("failed", fmtInt(m.http_req_failed.failed));
  setText("errRate", fmtPct(m.http_req_failed.rate));
  setText("latHint", "whole run (from the JSON report)");
  setText("avg", fmtMs(d.avg));
  setText("p50", d.count ? fmtMs(d.p50) : "–");
  setText("p90", d.count ? fmtMs(d.p90) : "–");
  setText("p95v", d.count ? fmtMs(d.p95) : "–");
  setText("p99", d.count ? fmtMs(d.p99) : "–");

  const codes = Object.entries(m.http_status_codes || {});
  $("codes").tBodies[0].replaceChildren(...(codes.length ? codes : [["–", 0]]).map(([c, n]) => row([c, fmtInt(n)])));

  const rows = rep.thresholds.map((t) => {
    const r = row([t.passed ? "✓" : "✗", t.metric, t.expression, t.observed == null ? "no data" : t.unit === "ms" ? fmtMs(t.observed) : String(Math.round(t.observed * 10000) / 10000)]);
    r.cells[0].className = t.passed ? "pass" : "fail";
    return r;
  });
  for (const c of rep.checks) {
    const r = row([c.fails ? "✗" : "✓", "check", c.name, `${fmtInt(c.passes)} passed, ${fmtInt(c.fails)} failed`]);
    r.cells[0].className = c.fails ? "fail" : "pass";
    rows.push(r);
  }
  $("thresholds").tBodies[0].replaceChildren(...rows);
  $("finalBlock").hidden = false;
}

function row(cells) {
  const tr = document.createElement("tr");
  for (const c of cells) tr.append(Object.assign(document.createElement("td"), { textContent: c }));
  return tr;
}

async function poll() {
  if (!current) return;
  try {
    const st = await api(`/api/runs/${current}`);
    render(st);
    if (st.state !== "running") {
      stopPolling();
      return;
    }
  } catch (e) {
    setText("runError", e.message);
  }
  timer = setTimeout(poll, 1000);
}

function stopPolling() {
  clearTimeout(timer);
  $("startBtn").disabled = false;
  $("cancelBtn").disabled = true;
}

async function download(kind) {
  const res = await fetch(`/api/runs/${current}/report.${kind}`, { headers: { "X-LoadTool-Token": token } });
  if (!res.ok) { setText("runError", `report: HTTP ${res.status}`); return; }
  const url = URL.createObjectURL(await res.blob());
  const a = Object.assign(document.createElement("a"), { href: url, download: `loadtool-report.${kind}` });
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

document.addEventListener("DOMContentLoaded", () => {
  if (!token) setText("formError", "No session token: open the URL that loadtool ui printed.");
  document.querySelectorAll("[data-add]").forEach((b) => b.addEventListener("click", () => addField(b.dataset.add)));
  $("authType").addEventListener("change", () => {
    const t = $("authType").value;
    document.querySelectorAll(".auth").forEach((el) => { el.hidden = !el.classList.contains(t); });
  });

  $("preflightBtn").addEventListener("click", async () => {
    setText("formError", "");
    $("preflightBtn").disabled = true;
    try {
      const pv = await api("/api/preflight", requestFromForm());
      $("preflight").hidden = false;
      setText("pfSummary", pv.error ? `No response: ${pv.error}` : `${pv.status} in ${fmtMs(pv.durationMs)} (${pv.proto})`);
      const hdrs = Object.entries(pv.headers || {}).map(([k, v]) => `${k}: ${v}`).join("\n");
      setText("pfDetail", `${hdrs}\n\n${pv.body || ""}${pv.truncated ? "\n… (first 2 KB)" : ""}`);
    } catch (e) {
      setText("formError", e.message);
    } finally {
      $("preflightBtn").disabled = false;
    }
  });

  $("form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    setText("formError", "");
    try {
      const { id } = await api("/api/runs", runFromForm());
      current = id;
      $("finalBlock").hidden = true;
      $("startBtn").disabled = true;
      $("cancelBtn").disabled = false;
      poll();
    } catch (e) {
      setText("formError", e.message);
    }
  });

  $("cancelBtn").addEventListener("click", async () => {
    if (!current) return;
    $("cancelBtn").disabled = true;
    try { await api(`/api/runs/${current}/cancel`, {}); } catch (e) { setText("runError", e.message); }
  });
  $("dlJson").addEventListener("click", () => download("json"));
  $("dlHtml").addEventListener("click", () => download("html"));
});
