"use strict";

// Minimal dashboard glue: fetch all endpoints once, render cards/charts/tables.
// No build step — vanilla JS plus Chart.js from CDN.

const fmtUSD = (n) => "$" + (Number(n) || 0).toFixed(4);
const fmtUSD2 = (n) => "$" + (Number(n) || 0).toFixed(2);
const fmtInt = (n) => (Number(n) || 0).toLocaleString();

async function getJSON(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(url + " -> " + res.status);
  return res.json();
}

// ---- profile filter ----
// Empty string means "all profiles". withProfile() appends it to any API URL.
let currentProfile = "";

function withProfile(url) {
  if (!currentProfile) return url;
  return url + (url.includes("?") ? "&" : "?") + "profile=" + encodeURIComponent(currentProfile);
}

// ---- tabs ----
document.getElementById("tabs").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  document.querySelectorAll("#tabs button").forEach((b) => b.classList.remove("active"));
  document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
  btn.classList.add("active");
  document.getElementById(btn.dataset.tab).classList.add("active");
});

// Chart instances are kept so a profile-filter reload can destroy the old chart
// before drawing a new one on the same canvas (Chart.js rejects a reused canvas).
const charts = {};
function drawChart(key, canvas, config) {
  if (charts[key]) charts[key].destroy();
  charts[key] = new Chart(canvas, config);
}

// ---- overview ----
function renderStats(s) {
  const cards = [
    ["Total cost", fmtUSD2(s.total_cost_usd)],
    ["Sessions", fmtInt(s.total_sessions)],
    ["Input tokens", fmtInt(s.total_input_tokens)],
    ["Output tokens", fmtInt(s.total_output_tokens)],
    ["Avg cost / session", fmtUSD(s.avg_cost_per_session)],
  ];
  document.getElementById("stat-cards").innerHTML = cards
    .map(([label, value]) => `<div class="card"><div class="label">${label}</div><div class="value">${value}</div></div>`)
    .join("");
}

function renderTimeline(buckets) {
  const empty = document.getElementById("timeline-empty");
  const canvas = document.getElementById("timeline-chart");
  if (!buckets || buckets.length === 0) {
    empty.hidden = false;
    canvas.hidden = true;
    return;
  }
  empty.hidden = true;
  canvas.hidden = false;
  drawChart("timeline", canvas, {
    type: "bar",
    data: {
      labels: buckets.map((b) => b.date),
      datasets: [{ label: "Daily cost (USD)", data: buckets.map((b) => b.cost_usd), backgroundColor: "#6ea8fe" }],
    },
    options: { responsive: true, plugins: { legend: { display: false } }, scales: { y: { beginAtZero: true } } },
  });
}

// ---- sessions ----
let sessionSort = "cost";

async function renderSessions() {
  const sessions = await getJSON(withProfile("/api/sessions?sort=" + sessionSort + "&limit=200"));
  const tbody = document.querySelector("#sessions-table tbody");
  const empty = document.getElementById("sessions-empty");
  tbody.innerHTML = "";
  if (!sessions || sessions.length === 0) {
    empty.hidden = false;
    return;
  }
  empty.hidden = true;
  for (const s of sessions) {
    const when = s.ended_at || s.started_at || "—";
    const skills = (s.skills || []).map((k) => `<span class="chip">${k}</span>`).join(" ") || "—";
    const tr = document.createElement("tr");
    tr.className = "session-row";
    tr.innerHTML =
      `<td>${when}</td><td>${s.profile || "default"}</td><td title="${s.cwd || ""}">${shortPath(s.cwd)}</td>` +
      `<td>${s.model || "—"}</td><td class="num">${fmtUSD(s.cost_usd)}</td><td>${skills}</td>`;
    tr.addEventListener("click", () => toggleDetail(tr, s.id));
    tbody.appendChild(tr);
  }
}

function shortPath(p) {
  if (!p) return "—";
  const parts = p.split("/").filter(Boolean);
  return parts.length <= 2 ? p : ".../" + parts.slice(-2).join("/");
}

async function toggleDetail(row, id) {
  const next = row.nextElementSibling;
  if (next && next.classList.contains("detail")) {
    next.remove();
    return;
  }
  const detail = await getJSON("/api/sessions/" + encodeURIComponent(id));
  const tools = (detail.tool_events || []).reduce((acc, e) => {
    acc[e.name] = (acc[e.name] || 0) + 1;
    return acc;
  }, {});
  const toolChips = Object.entries(tools)
    .sort((a, b) => b[1] - a[1])
    .map(([name, n]) => `<span class="chip">${name} ×${n}</span>`)
    .join(" ") || "none";
  const skillChips = (detail.skill_events || [])
    .map((e) => `<span class="chip">${e.name}</span>`)
    .join(" ") || "none";
  const tr = document.createElement("tr");
  tr.className = "detail";
  tr.innerHTML = `<td colspan="6"><strong>Skills:</strong> ${skillChips}<br><strong>Tools:</strong> ${toolChips}</td>`;
  row.after(tr);
}

document.querySelectorAll("#sessions-table th[data-sort]").forEach((th) => {
  th.addEventListener("click", () => {
    sessionSort = th.dataset.sort;
    renderSessions();
  });
});

// ---- skills ----
function renderSkills(skills) {
  const tbody = document.querySelector("#skills-table tbody");
  const empty = document.getElementById("skills-empty");
  const canvas = document.getElementById("skills-chart");
  tbody.innerHTML = "";
  if (!skills || skills.length === 0) {
    empty.hidden = false;
    canvas.hidden = true;
    return;
  }
  empty.hidden = true;
  canvas.hidden = false;
  for (const s of skills) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      `<td>${s.skill_name}</td><td class="num">${fmtInt(s.usage_count)}</td>` +
      `<td class="num">${fmtInt(s.session_count)}</td><td class="num">${fmtUSD(s.avg_cost_usd)}</td>` +
      `<td class="num">${fmtUSD(s.total_cost_usd)}</td>`;
    tbody.appendChild(tr);
  }
  drawChart("skills", canvas, {
    type: "bar",
    data: {
      labels: skills.map((s) => s.skill_name),
      datasets: [{ label: "Total attributed cost (USD)", data: skills.map((s) => s.total_cost_usd), backgroundColor: "#6ea8fe" }],
    },
    options: { indexAxis: "y", responsive: true, plugins: { legend: { display: false } }, scales: { x: { beginAtZero: true } } },
  });
}

// ---- models ----
const palette = ["#6ea8fe", "#7ddf9b", "#ffd479", "#ff8fa3", "#b39ddb", "#80deea"];

function renderModels(models) {
  const tbody = document.querySelector("#models-table tbody");
  const empty = document.getElementById("models-empty");
  const canvas = document.getElementById("models-chart");
  tbody.innerHTML = "";
  if (!models || models.length === 0) {
    empty.hidden = false;
    canvas.hidden = true;
    return;
  }
  empty.hidden = true;
  canvas.hidden = false;
  for (const m of models) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      `<td>${m.model}</td><td class="num">${fmtInt(m.session_count)}</td>` +
      `<td class="num">${fmtInt(m.total_input_tokens)}</td><td class="num">${fmtInt(m.total_output_tokens)}</td>` +
      `<td class="num">${fmtUSD(m.total_cost_usd)}</td>`;
    tbody.appendChild(tr);
  }
  drawChart("models", canvas, {
    type: "doughnut",
    data: {
      labels: models.map((m) => m.model),
      datasets: [{ data: models.map((m) => m.total_cost_usd), backgroundColor: models.map((_, i) => palette[i % palette.length]) }],
    },
    options: { responsive: true, plugins: { legend: { position: "right" } } },
  });
}

// ---- profile selector ----
// Populate the dropdown once; only reveal it when more than one profile has
// recorded sessions (a single-profile install needs no filter UI).
async function initProfiles() {
  const control = document.getElementById("profile-control");
  const select = document.getElementById("profile-select");
  try {
    const profiles = await getJSON("/api/profiles");
    if (!profiles || profiles.length <= 1) return;
    select.innerHTML =
      `<option value="">All profiles</option>` +
      profiles.map((p) => `<option value="${p}">${p}</option>`).join("");
    select.value = currentProfile;
    select.addEventListener("change", () => {
      currentProfile = select.value;
      loadAll();
    });
    control.hidden = false;
  } catch (err) {
    console.error("profiles load failed:", err);
  }
}

// ---- boot ----
// loadAll fetches every endpoint for the current profile and re-renders. It is
// re-run whenever the profile filter changes.
async function loadAll() {
  try {
    const [stats, timeline, skills, models] = await Promise.all([
      getJSON(withProfile("/api/stats")),
      getJSON(withProfile("/api/timeline?days=30")),
      getJSON(withProfile("/api/skills")),
      getJSON(withProfile("/api/models")),
    ]);
    renderStats(stats);
    renderTimeline(timeline);
    renderSkills(skills);
    renderModels(models);
    await renderSessions();
  } catch (err) {
    console.error("dashboard load failed:", err);
  }
}

async function boot() {
  await initProfiles();
  await loadAll();
}

boot();
