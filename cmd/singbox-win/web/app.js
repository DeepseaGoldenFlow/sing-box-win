const dictionary = {
"Dashboard":["仪表盘","Dashboard"],"Profiles":["配置","Profiles"],"Connections":["连接","Connections"],"Logs":["日志","Logs"],"Settings":["设置","Settings"],
"upload":["上传速度","Upload"],"download":["下载速度","Download"],"network":["网络","Network"],"duration":["时长","Duration"],"clear":["清空","Clear"],"copyLink":["复制导入链接","Copy import link"],"language":["语言","Language"],"theme":["主题","Theme"],"dark":["深色","Dark"],"light":["浅色","Light"],"register":["注册深链","Register deep links"],"unregister":["取消注册","Unregister"],"exportLogs":["导出日志","Export logs"],"quit":["退出客户端","Quit"],"actions":["操作","Actions"],"saved":["已保存","Saved"],"connected":["运行中","Connected"],"stopped":["已停止","Stopped"],"noProfile":["尚未选择配置","No profile selected"],"test":["测速","Test latency"],"urlRequired":["请先输入订阅 URL","Enter a subscription URL first"],
};
Object.assign(dictionary,{chooseProfile:["去选择配置","Choose a profile"],countUnit:["个","items"],trafficEmpty:["暂无流量数据，启动后开始采样","No traffic data. Start to begin sampling."]});
let language="zh-CN";
function t(key){return dictionary[key]?.[language==="en"?1:0] || key;}
function applyLanguage(value){language=value==="en"?"en":"zh-CN";document.documentElement.lang=language;document.querySelectorAll("[data-i18n]").forEach(el=>{if(el.matches("nav button"))return;el.textContent=t(el.dataset.i18n);});document.querySelectorAll("[data-i18n-placeholder]").forEach(el=>el.placeholder=t(el.dataset.i18nPlaceholder));if(document.getElementById("title"))document.getElementById("title").textContent=t(page);}
function icon(name){const paths={Dashboard:'<path d="M3 17a9 9 0 1 1 18 0M12 13l4-5M5 17h14"/>',Profiles:'<path d="M6 3h8l4 4v14H6zM14 3v5h4M9 12h6M9 16h6"/>',Connections:'<path d="m9 15 6-6M8 16l-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0m4 0 2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0" transform="translate(1 -1)"/>',Logs:'<path d="M8 6h12M8 12h12M8 18h12M3 6h1M3 12h1M3 18h1"/>',Settings:'<path d="m9 3-1 3-3 1-2 3 2 2-1 4 3 2 3-1 3 2 3-1 1-3 3-2-1-4-3-1-1-3z"/><circle cx="11" cy="11" r="3"/>'};const svg=document.createElementNS("http://www.w3.org/2000/svg","svg");svg.setAttribute("viewBox","0 0 24 24");svg.setAttribute("fill","none");svg.setAttribute("stroke","currentColor");svg.setAttribute("stroke-width","1.6");svg.setAttribute("aria-hidden","true");svg.innerHTML=paths[name]||'<path d="M12 4v16m-6-6 6 6 6-6"/>';return svg;}
Object.assign(dictionary,{"关闭窗口后继续运行": ["关闭窗口后继续运行", "Runs in background when closed"], "退出客户端": ["退出客户端", "Quit"], "启动": ["启动", "Start"], "停止": ["停止", "Stop"], "重启": ["重启", "Restart"], "系统代理": ["系统代理", "System proxy"], "TUN 模式待支持 · 关闭窗口后继续运行": ["TUN 模式待支持 · 关闭窗口后继续运行", "TUN support pending · Runs in background when closed"], "连接数": ["连接数", "Connections"], "累计上传 0 B / 累计下载 0 B": ["累计上传 0 B / 累计下载 0 B", "Total upload 0 B / Total download 0 B"], "节点与分组": ["节点与分组", "Nodes & groups"], "刷新列表": ["刷新列表", "Refresh"], "全部测速": ["全部测速", "Test all"], "导入配置": ["导入配置", "Import profile"], "订阅 URL": ["订阅 URL", "Subscription URL"], "Windows 文件路径": ["Windows 文件路径", "Windows file path"], "粘贴 JSON": ["粘贴 JSON", "Paste JSON"], "导入": ["导入", "Import"], "从剪贴板粘贴": ["从剪贴板粘贴", "Paste from clipboard"], "关闭全部连接": ["关闭全部连接", "Close all connections"], "规则": ["规则", "Rule"], "全部级别": ["全部级别", "All levels"], "自动滚动": ["自动滚动", "Auto scroll"], "客户端设置": ["客户端设置", "Client settings"], "开机自启": ["开机自启", "Launch at sign-in"], "启动后自动连接": ["启动后自动连接", "Auto connect"], "允许局域网访问代理端口": ["允许局域网访问代理端口", "Allow LAN"], "本地代理端口": ["本地代理端口", "Mixed port"], "订阅更新间隔（小时，0 为关闭）": ["订阅更新间隔（小时，0 为关闭）", "Update interval (hours, 0 disables)"], "保存设置": ["保存设置", "Save settings"], "配置名称": ["配置名称", "Profile name"], "输入订阅 URL、文件路径或配置内容": ["输入订阅 URL、文件路径或配置内容", "Subscription URL, file path or JSON"], "搜索日志": ["搜索日志", "Search logs"], "搜索连接": ["搜索连接", "Search connections"], "停用": ["停用", "Disable"], "启用": ["启用", "Enable"], "改名": ["改名", "Rename"], "刷新": ["刷新", "Update"], "删除": ["删除", "Delete"], "关闭": ["关闭", "Close"], "订阅": ["订阅", "Subscription"], "本地": ["本地", "Local"]});
"use strict";
const $ = (id) => document.getElementById(id);
let page = "Dashboard",
  running = false,
  last = null,
  history = [],
  nodeTags = [];
function error(e) {
  $("error").textContent = String(e.message || e).slice(0, 500);
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
  b.textContent = t(text);
  b.dataset.i18n = text;
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
    $("title").textContent = t(title);
    if (title === "Profiles") await profiles();
    if (title === "Settings") await settings();
  });
  const label = document.createElement("span"); label.dataset.i18n=title; label.textContent=t(title); b.replaceChildren(icon(title),label);
  b.dataset.page=title;
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
      (p.type === "remote" ? t("订阅") : t("本地")) +
      " · " +
      new Date(p.updated).toLocaleString();
    info.append(name, meta);
    card.append(info);
    const controls = document.createElement("div");
    controls.className = "row";
    const menu=document.createElement("details"), summary=document.createElement("summary");summary.textContent=t("actions");menu.append(summary,controls);
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
    card.append(menu);
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
        data[el.name] = el.type === "checkbox" ? el.checked : el.type === "number" ? Number(el.value) : el.value;
    }
    await api("settings", "POST", data);
    applyLanguage(data.language); document.documentElement.dataset.theme=data.theme; $("saved").textContent = t("saved");
  });
};
$("closeAll").onclick = () => action(() => api("connections", "DELETE"));
async function connections() {
  const data = await api("connections");
  $("connections").replaceChildren();
  for (const c of data.connections || []) {
    if (!JSON.stringify(c.metadata).toLowerCase().includes($("connectionSearch").value.toLowerCase())) continue;
    const tr = document.createElement("tr");
    for (const value of [
      c.metadata.host || c.metadata.destinationIP,
      c.rule + " " + (c.rulePayload || ""),
      c.metadata.network || "tcp",bytes(c.upload), bytes(c.download),Math.round((Date.now() - new Date(c.start)) / 1000) + "s",
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
  const data=await api("node/list"); $("nodes").replaceChildren();nodeTags=[];
  const entries=Object.entries(data.proxies||{}), hasGroups=entries.some(([,n])=>n.all?.length);
  for(const [tag,n] of entries){
    if(hasGroups&&!n.all?.length)continue;
    const row=document.createElement("div");row.className="nodeGroup";
    const h=document.createElement("h2");h.textContent=tag+" · "+n.type;row.append(h);
    for(const member of n.all?.length?n.all:[tag]){
      if(!nodeTags.includes(member))nodeTags.push(member);
      const line=document.createElement("div");line.className="node";
      const select=button(member,async()=>{if(n.all?.length){await api("node/select","PUT",{group:tag,name:member});await nodes();}});
      select.removeAttribute("data-i18n");select.classList.toggle("selected",member===n.now); select.disabled=!running;
      const history=data.proxies[member]?.history||[];
      const delay=button(history.length?history.at(-1).delay+" ms":t("test"),async()=>{const d=await api("node/delay","POST",{tag:member});delay.textContent=d.delay+" ms";delay.removeAttribute("data-i18n");});
      delay.dataset.node=member;delay.disabled=!running;line.append(select,delay);row.append(line);
    }$("nodes").append(row);
  }
}
$("nodesRefresh").onclick=()=>action(nodes);
$("delayAll").onclick=()=>action(async()=>{for(const tag of nodeTags){try{const d=await api("node/delay","POST",{tag});for(const el of document.querySelectorAll("[data-node]")){if(el.dataset.node===tag)el.textContent=d.delay+" ms";}}catch(e){error(e);}}});
async function poll() {
  const s = await api("state");
  running = s.running;
  $("connectionState").textContent=t(s.running?"connected":"stopped");
  for(const b of document.querySelectorAll("[data-core]")){b.classList.toggle("primary",b.dataset.core==="start");b.disabled=b.dataset.core==="start"?running:!running;}
  $("status").textContent =
    (s.running ? t("connected") : t("stopped")) + " · v" + s.version;
  $("profile").textContent = s.profile || t("noProfile");
  $("chooseProfile").hidden = !!s.profile;
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
    setRate("up",up);
    setRate("down",down);
    $("countValue").textContent = t.connections;
    $("totals").textContent =
      (language==="en"?"Total upload ":"累计上传 ") + bytes(t.upload_total) + (language==="en"?" / Download ":" / 累计下载 ") + bytes(t.download_total);
    history.push([up, down, now]);
    history = history.filter(v=>now-v[2]<60000);
    if (history.length > 60) history.shift();
    renderChart(now);
  }
  if (!running) {last=null;setRate("up",0);setRate("down",0);$("countValue").textContent="0";renderChart(Date.now());}
  if(page==="Dashboard" && (!window.nodesLoaded || window.nodesRunning!==running)){window.nodesLoaded=true;window.nodesRunning=running;await nodes();}
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
async function init(){const s=await api("settings");applyLanguage(s.language);document.documentElement.dataset.theme=s.theme;if(location.hash==="#Profiles")document.querySelector('[data-page="Profiles"]').click();await poll();}init().catch(error);
$("language").onchange=()=>applyLanguage($("language").value);
$("theme").onchange=()=>document.documentElement.dataset.theme=$("theme").value;
$("clearLogs").onclick=()=>action(()=>api("logs","DELETE"));
$("register").onclick=()=>action(()=>api("protocol","POST",{enabled:true}));
$("unregister").onclick=()=>action(()=>api("protocol","POST",{enabled:false}));
$("settingsQuit").onclick=()=>$("quit").click();
$("copyLink").onclick=()=>action(async()=>{const u=$("input").value.trim();if(!/^https?:\/\//.test(u))throw Error(t("urlRequired"));await navigator.clipboard.writeText("singboxwin://install-config?url="+encodeURIComponent(u));});
$("exportLogs").onclick=()=>action(async()=>{const logs=await api("logs?limit=5000");const u=URL.createObjectURL(new Blob([logs.map(l=>l.time+" ["+l.level+"] "+l.message).join("\n")],{type:"text/plain"}));const a=document.createElement("a");a.href=u;a.download="sing-box-win.log";a.click();URL.revokeObjectURL(u);});

for(const el of document.querySelectorAll("[data-arrow]")){el.append(icon("arrow"));if(el.dataset.arrow==="up")el.firstChild.style.transform="rotate(180deg)";}

function setRate(id,n){const [value,unit]=bytes(n).split(" ");const small=document.createElement("span");small.className="unit";small.textContent=" "+unit+"/s";$(id).replaceChildren(document.createTextNode(value),small);}

$("chooseProfile").onclick=()=>document.querySelector('[data-page="Profiles"]').click();
$("countIcon").append(icon("Connections"));
function renderChart(now){
  const samples=history.filter(v=>now-v[2]<60000);
  const peak=Math.max(0,...samples.flatMap(v=>v.slice(0,2)));
  const max=peak ? Math.pow(10,Math.floor(Math.log10(peak)))*Math.ceil(peak/Math.pow(10,Math.floor(Math.log10(peak)))) : 1024;
  const unit=max>=1048576?1048576:max>=1024?1024:1;
  const suffix=unit===1048576?"MB/s":unit===1024?"KB/s":"B/s";
  $("yAxis").replaceChildren(...[1,.75,.5,.25,0].map(f=>{const el=document.createElement("span");el.textContent=Number((max*f/unit).toFixed(2))+" "+suffix;return el;}));
  $("chartEmpty").hidden=samples.length>0;
  for(const [idx,id] of [[0,"upLine"],[1,"downLine"]])$(id).setAttribute("points",samples.map(v=>(800-(now-v[2])*800/60000)+","+(158-v[idx]/max*156)).join(" "));
}
renderChart(Date.now());
