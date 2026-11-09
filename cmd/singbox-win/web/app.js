"use strict";
const $ = (id) => document.getElementById(id);
let page = "Dashboard",
  running = false,
  last = null,
  history = [],
  nodeTags = [];
function error(e) {
  $("error").textContent = e.message || e;
  $("error").style.display = "block";
}
async function api(path, method = "GET", body) {
  const r = await fetch("/api/" + path, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) throw Error(await r.text());
  return r.json();
}
function button(text, fn) {
  const b = document.createElement("button");
  b.textContent = text;
  b.onclick = () => action(fn);
  return b;
}
async function action(fn) {
  try {
    $("error").style.display = "none";
    await fn();
    await poll();
  } catch (e) {
    error(e);
  }
}
for (const title of [
  "Dashboard",
  "Profiles",
  "Connections",
  "Logs",
  "Settings",
]) {
  const b = button(title, async () => {
    page = title;
    for (const s of document.querySelectorAll("main>section"))
      s.hidden = s.id !== title;
    for (const n of document.querySelectorAll("nav button"))
      n.classList.toggle("active", n === b);
    $("title").textContent = title;
    if (title === "Profiles") await profiles();
    if (title === "Settings") await settings();
  });
  if (title === page) b.className = "active";
  document.querySelector("nav").append(b);
}
for (const b of document.querySelectorAll("[data-core]"))
  b.onclick = () => action(() => api("core/" + b.dataset.core, "POST"));
$("proxy").onchange = () =>
  action(() => api("proxy", "POST", { enabled: $("proxy").checked }));
$("quit").onclick = () =>
  action(async () => {
    await api("quit", "POST");
    document.body.textContent = "客户端已退出，可以关闭此窗口。";
  });
function bytes(n) {
  n = Number(n) || 0;
  const units = ["B", "KiB", "MiB", "GiB"];
  let i = 0;
  while (n >= 1024 && i < 3) {
    n /= 1024;
    i++;
  }
  return n.toFixed(i ? 1 : 0) + " " + units[i];
}
async function profiles() {
  const data = await api("profiles");
  $("profiles").replaceChildren();
  for (const p of data || []) {
    const card = document.createElement("div");
    card.className = "card profileRow";
    const info = document.createElement("div"),
      name = document.createElement("h2"),
      meta = document.createElement("p");
    name.textContent = (p.enabled ? "● " : "") + p.name;
    meta.textContent =
      (p.type === "remote" ? "订阅" : "本地") +
      " · " +
      new Date(p.updated).toLocaleString();
    info.append(name, meta);
    card.append(info);
    const controls = document.createElement("div");
    controls.className = "row";
    controls.append(
      button(p.enabled ? "停用" : "启用", async () => {
        await api("profiles/" + p.id, "PUT", { enabled: !p.enabled });
        await profiles();
      }),
      button("改名", async () => {
        const name = prompt("配置名称", p.name);
        if (name) {
          await api("profiles/" + p.id, "PUT", { name });
          await profiles();
        }
      }),
    );
    if (p.type === "remote")
      controls.append(
        button("刷新", async () => {
          await api("profiles/" + p.id + "/update", "POST");
          await profiles();
        }),
      );
    controls.append(
      button("删除", async () => {
        if (confirm("删除配置 " + p.name + "？")) {
          await api("profiles/" + p.id, "DELETE");
          await profiles();
        }
      }),
    );
    card.append(controls);
    $("profiles").append(card);
  }
}
$("import").onclick = () =>
  action(async () => {
    await api("profiles/import", "POST", {
      name: $("name").value,
      [$("source").value]: $("input").value,
    });
    $("input").value = "";
    await profiles();
  });
$("clipboard").onclick = () =>
  action(async () => {
    $("input").value = await navigator.clipboard.readText();
  });
$("file").onchange = () =>
  action(async () => {
    const file = $("file").files[0];
    if (file) {
      $("source").value = "content";
      $("input").value = await file.text();
      $("name").value = file.name;
    }
  });
async function settings() {
  const s = await api("settings");
  for (const el of $("settings").elements) {
    if (!el.name) continue;
    if (el.type === "checkbox") el.checked = s[el.name];
    else el.value = s[el.name];
  }
}
$("settings").onsubmit = (e) => {
  e.preventDefault();
  action(async () => {
    const data = {};
    for (const el of $("settings").elements) {
      if (el.name)
        data[el.name] = el.type === "checkbox" ? el.checked : Number(el.value);
    }
    await api("settings", "POST", data);
    $("saved").textContent = "已保存";
  });
};
$("closeAll").onclick = () => action(() => api("connections", "DELETE"));
async function connections() {
  const data = await api("connections");
  $("connections").replaceChildren();
  for (const c of data.connections || []) {
    const tr = document.createElement("tr");
    for (const value of [
      c.metadata.host || c.metadata.destinationIP,
      c.rule + " " + (c.rulePayload || ""),
      Math.round((Date.now() - new Date(c.start)) / 1000) + "s",
      bytes(c.upload) + " / " + bytes(c.download),
    ]) {
      const td = document.createElement("td");
      td.textContent = value;
      tr.append(td);
    }
    const td = document.createElement("td");
    td.append(
      button("关闭", () =>
        api("connections/" + encodeURIComponent(c.id), "DELETE"),
      ),
    );
    tr.append(td);
    $("connections").append(tr);
  }
}
async function nodes() {
  const data = await api("nodes");
  $("nodes").replaceChildren();
  nodeTags = [];
  for (const [tag, n] of Object.entries(data.proxies || {})) {
    nodeTags.push(tag);
    const row = document.createElement("div");
    row.className = "node";
    const label = document.createElement("span");
    label.textContent = tag + " · " + n.type + (n.now ? " → " + n.now : "");
    const b = button("测速", async () => {
      const d = await api("node/delay", "POST", { tag });
      b.textContent = d.delay + " ms";
    });
    row.append(label, b);
    $("nodes").append(row);
  }
}
$("nodesRefresh").onclick = () => action(nodes);
$("delayAll").onclick = () =>
  action(async () => {
    for (const b of $("nodes").querySelectorAll("button")) await b.onclick();
  });
async function poll() {
  const s = await api("state");
  running = s.running;
  $("status").textContent =
    (s.running ? "● 运行中" : "○ 已停止") + " · v" + s.version;
  $("profile").textContent = s.profile || "尚未选择配置";
  $("proxy").checked = s.system_proxy;
  if (s.error) error(s.error);
  if (page === "Logs") {
    const data = await api(
      "logs?" +
        new URLSearchParams({
          level: $("level").value,
          search: $("search").value,
          limit: 1000,
        }),
    );
    $("logs").textContent = data
      .map((l) => l.time + " [" + l.level + "] " + l.message)
      .join("\n");
    if ($("autoscroll").checked) $("logs").scrollTop = $("logs").scrollHeight;
  }
  if (running && page === "Connections") await connections();
  if (running && page === "Dashboard") {
    const t = await api("traffic"),
      now = Date.now();
    const up = last
        ? Math.max(
            0,
            (t.upload_total - last.upload_total) / ((now - last.time) / 1000),
          )
        : 0,
      down = last
        ? Math.max(
            0,
            (t.download_total - last.download_total) /
              ((now - last.time) / 1000),
          )
        : 0;
    last = { ...t, time: now };
    $("up").textContent = bytes(up) + "/s";
    $("down").textContent = bytes(down) + "/s";
    $("count").textContent = t.connections;
    $("totals").textContent =
      "累计 ↑ " + bytes(t.upload_total) + " / ↓ " + bytes(t.download_total);
    history.push([up, down]);
    if (history.length > 60) history.shift();
    const max = Math.max(1, ...history.flat());
    for (const [idx, id] of [
      [0, "upLine"],
      [1, "downLine"],
    ])
      $(id).setAttribute(
        "points",
        history
          .map((v, i) => (i * 800) / 59 + "," + (145 - (v[idx] / max) * 140))
          .join(" "),
      );
  }
  if (!running) last = null;
}
let polling = false;
setInterval(async () => {
  if (polling) return;
  polling = true;
  try {
    await poll();
  } catch (e) {
    error(e);
  } finally {
    polling = false;
  }
}, 1000);
poll().catch(error);
