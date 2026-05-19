const state = {
  timer: null,
  history: [],
};

const $ = (selector) => document.querySelector(selector);
const fmtBytes = (n) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = Number(n || 0);
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i += 1;
  }
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
};

document.querySelectorAll(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    document.querySelectorAll(".tab, .panel").forEach((el) => el.classList.remove("active"));
    tab.classList.add("active");
    $(`#${tab.dataset.panel}`).classList.add("active");
  });
});

async function requestJSON(url, options = {}) {
  const resp = await fetch(url, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  const data = await resp.json();
  if (!resp.ok || data.ok === false) {
    throw new Error(data.error || `HTTP ${resp.status}`);
  }
  return data;
}

async function loadTraffic() {
  try {
    const data = await requestJSON("/api/traffic");
    const flows = data.flows || [];
    renderTraffic(flows);
    $("#status").textContent = "已连接";
  } catch (err) {
    $("#status").textContent = "异常";
    renderResult(`流量接口错误: ${err.message}`);
  }
}

function renderTraffic(flows) {
  const totalBytes = flows.reduce((sum, row) => sum + Number(row.bytes || 0), 0);
  const peak = flows.reduce((max, row) => Math.max(max, Number(row.peak_bps || 0)), 0);
  const nowRate = flows.reduce((sum, row) => sum + Number(row.current_bps || 0), 0);
  $("#flowCount").textContent = flows.length;
  $("#totalBytes").textContent = fmtBytes(totalBytes);
  $("#peakRate").textContent = `${fmtBytes(peak)}/s`;

  state.history.push({ t: new Date(), v: nowRate });
  state.history = state.history.slice(-60);
  drawChart(state.history);

  $("#trafficRows").innerHTML = flows
    .slice()
    .sort((a, b) => Number(b.current_bps || 0) - Number(a.current_bps || 0))
    .slice(0, 100)
    .map((row) => `
      <tr>
        <td>${escapeHTML(row.src_ip || "")}</td>
        <td>${escapeHTML(row.dst_ip || "")}</td>
        <td>${escapeHTML(row.proto || "")}</td>
        <td>${row.packets || 0}</td>
        <td>${fmtBytes(row.bytes)}</td>
        <td>${fmtBytes(row.current_bps)}/s</td>
        <td>${fmtBytes(row.peak_bps)}/s</td>
        <td>${fmtBytes(row.avg_2s_bps)}/s</td>
        <td>${fmtBytes(row.avg_10s_bps)}/s</td>
        <td>${fmtBytes(row.avg_40s_bps)}/s</td>
      </tr>
    `)
    .join("");
}

function drawChart(points) {
  const canvas = $("#trafficChart");
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(600, Math.floor(rect.width * dpr));
  canvas.height = Math.floor(260 * dpr);
  const ctx = canvas.getContext("2d");
  const w = canvas.width;
  const h = canvas.height;
  ctx.clearRect(0, 0, w, h);
  ctx.scale(dpr, dpr);

  const width = w / dpr;
  const height = h / dpr;
  const pad = 36;
  const max = Math.max(1, ...points.map((p) => p.v));

  ctx.strokeStyle = "#d7dde5";
  ctx.lineWidth = 1;
  for (let i = 0; i < 5; i += 1) {
    const y = pad + ((height - pad * 2) * i) / 4;
    ctx.beginPath();
    ctx.moveTo(pad, y);
    ctx.lineTo(width - pad, y);
    ctx.stroke();
  }

  ctx.strokeStyle = "#0f766e";
  ctx.lineWidth = 2;
  ctx.beginPath();
  points.forEach((point, idx) => {
    const x = pad + ((width - pad * 2) * idx) / Math.max(1, points.length - 1);
    const y = height - pad - ((height - pad * 2) * point.v) / max;
    if (idx === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  });
  ctx.stroke();

  ctx.fillStyle = "#475569";
  ctx.font = "12px sans-serif";
  ctx.fillText(`${fmtBytes(max)}/s`, pad, 18);
  ctx.fillText("当前总速率", width - 102, height - 12);
}

async function loadRules() {
  try {
    const data = await requestJSON("/api/firewall/list");
    renderRules(data.rules || []);
  } catch (err) {
    renderResult(`规则列表错误: ${err.message}`);
  }
}

function renderRules(rules) {
  $("#ruleRows").innerHTML = rules.map((rule) => `
    <tr>
      <td>${escapeHTML(rule.name || "")}</td>
      <td>${escapeHTML(rule.proto || "")}</td>
      <td>${escapeHTML(rule.src_ip || "")}</td>
      <td>${escapeHTML(rule.dest_ip || "")}</td>
      <td>${escapeHTML(rule.port || "")}</td>
      <td>${escapeHTML(rule.action || "")}</td>
      <td><button data-delete="${escapeHTML(rule.name || "")}">删除</button></td>
    </tr>
  `).join("");
  document.querySelectorAll("[data-delete]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await postAction("/api/firewall/delete", { name: btn.dataset.delete });
      await loadRules();
    });
  });
}

async function postAction(url, body) {
  try {
    const data = await requestJSON(url, { method: "POST", body: JSON.stringify(body) });
    renderResult(JSON.stringify(data, null, 2));
    return data;
  } catch (err) {
    renderResult(err.message);
    throw err;
  }
}

$("#ruleForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const body = Object.fromEntries(form.entries());
  Object.keys(body).forEach((key) => {
    body[key] = String(body[key]).trim();
    if (!body[key]) delete body[key];
  });
  await postAction("/api/firewall/add", body);
  await loadRules();
});

$("#verifyForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const body = Object.fromEntries(new FormData(event.currentTarget).entries());
  await postAction("/api/firewall/verify", body);
});

$("#reloadRules").addEventListener("click", loadRules);
$("#clearRules").addEventListener("click", async () => {
  if (!confirm("确认清空本实验创建的防火墙规则？")) return;
  await postAction("/api/firewall/clear", {});
  await loadRules();
});
$("#refreshTraffic").addEventListener("click", loadTraffic);
$("#refreshInterval").addEventListener("change", () => {
  startTrafficTimer();
});

function startTrafficTimer() {
  if (state.timer) clearInterval(state.timer);
  const ms = Number($("#refreshInterval").value);
  state.timer = setInterval(loadTraffic, ms);
}

function renderResult(text) {
  $("#resultBox").textContent = text;
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[ch]));
}

loadTraffic();
loadRules();
startTrafficTimer();
