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

// ---- tabs ----
document.getElementById("tabs").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  document.querySelectorAll("#tabs button").forEach((b) => b.classList.remove("active"));
  document.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
  btn.classList.add("active");
  document.getElementById(btn.dataset.tab).classList.add("active");
});

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
  new Chart(canvas, {
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
  const sessions = await getJSON("/api/sessions?sort=" + sessionSort + "&limit=200");
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
      `<td>${when}</td><td title="${s.cwd || ""}">${shortPath(s.cwd)}</td>` +
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
  tr.innerHTML = `<td colspan="5"><strong>Skills:</strong> ${skillChips}<br><strong>Tools:</strong> ${toolChips}</td>`;
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
  for (const s of skills) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      `<td>${s.skill_name}</td><td class="num">${fmtInt(s.usage_count)}</td>` +
      `<td class="num">${fmtInt(s.session_count)}</td><td class="num">${fmtUSD(s.avg_cost_usd)}</td>` +
      `<td class="num">${fmtUSD(s.total_cost_usd)}</td>`;
    tbody.appendChild(tr);
  }
  new Chart(canvas, {
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
  for (const m of models) {
    const tr = document.createElement("tr");
    tr.innerHTML =
      `<td>${m.model}</td><td class="num">${fmtInt(m.session_count)}</td>` +
      `<td class="num">${fmtInt(m.total_input_tokens)}</td><td class="num">${fmtInt(m.total_output_tokens)}</td>` +
      `<td class="num">${fmtUSD(m.total_cost_usd)}</td>`;
    tbody.appendChild(tr);
  }
  new Chart(canvas, {
    type: "doughnut",
    data: {
      labels: models.map((m) => m.model),
      datasets: [{ data: models.map((m) => m.total_cost_usd), backgroundColor: models.map((_, i) => palette[i % palette.length]) }],
    },
    options: { responsive: true, plugins: { legend: { position: "right" } } },
  });
}

// ---- boot ----
async function boot() {
  try {
    const [stats, timeline, skills, models] = await Promise.all([
      getJSON("/api/stats"),
      getJSON("/api/timeline?days=30"),
      getJSON("/api/skills"),
      getJSON("/api/models"),
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

boot();
