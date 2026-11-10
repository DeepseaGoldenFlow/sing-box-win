> ⚠️ **非官方客户端 / Unofficial client** — 本项目是第三方基于 [sing-box](https://github.com/SagerNet/sing-box) 内核开发的 Windows 原生图形客户端，**与 SagerNet 官方无任何关联**。sing-box / sing-box-for-apple 版权归 SagerNet 所有。本项目以 **GPL-3.0** 许可证开源。

# sing-box for Windows

sing-box for Windows 是纯 Go 单进程客户端：界面是原生 WinUI 3（`native/SingBoxWin.Native`），引擎始终以 `--headless` 提供 HTTP API，仅监听 `127.0.0.1` 随机端口；关闭窗口后后端继续运行。在 UI 点击「退出」可停止内核、恢复代理并退出。

## 构建

```sh
make build
make check
curl -fsSL "$SUBSCRIPTION_URL" -o /tmp/sub.json
SINGBOX_TEST_SUB=/tmp/sub.json make check
```

输出 `build/singbox-win.exe`，Windows amd64，`CGO_ENABLED=0`，无 cgo、Wails 或 WebView2 宿主。构建入口可使用 Go 1.24.5 的自动工具链功能，但实际编译使用 Go 1.26.10：真实订阅包含 `http_clients`，采用 sing-box 1.14.4（要求 Go ≥1.25.5），因此不能使用 Go 1.24.5 编译器直接编译。Makefile 固定工具链与 `with_gvisor,with_quic,with_utls,with_clash_api` 标签；Tailscale 必须启用 gVisor。离线构建需提前缓存工具链和依赖。

等价命令：

```sh
export GOTOOLCHAIN=go1.26.10
export GOFLAGS='-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api'
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/singbox-win.exe ./cmd/singbox-win
go vet ./...
```

## 使用与数据

启动后从 Profiles 导入 URL、本地文件路径、文件选择器或粘贴 JSON。启用配置后在 Dashboard 启动内核，再开启系统代理。切换启用配置和修改监听端口前需停止内核。Settings 可配置自动连接、开机自启、更新间隔和局域网监听。

数据目录 `%APPDATA%\sing-box-win\`：`settings.json` 存配置元数据及设置，`profiles/*.json` 存订阅原文，`runtime.json` 记录当前 HTTP 地址和 PID。URL、配置可能含凭据，请妥善保护此目录；仓库和构建产物不含订阅内容。设置和配置仅应由当前用户访问。

配置通过 sing-box `option.Options.UnmarshalJSONContext`（当前版本的原生 JSON 解析入口）解析，注册真实 Tailscale endpoint/DNS 实现；不转换成自定义 JSON 模型。运行副本保留 endpoint、路由和 outbound，过滤 TUN inbound（本期未提供管理员权限/wintun 支持），关闭配置中的 `set_system_proxy`，监听地址遵循 sing-box for Windows 允许局域网设置，并增加 sing-box for Windows 专用 mixed 入口（默认 2085）。原订阅不改写。

系统代理仅写当前用户 Internet Settings 的 `ProxyEnable`、`ProxyServer`，使用 WinINet 广播刷新；原值持久保存，在关闭代理、正常停止、退出或下次启动时恢复。强制结束进程无法执行即时恢复；下次启动会恢复。已有其他代理软件可能同时写这些值，建议避免同时切换系统代理。

Clash 控制器另开随机 loopback 端口并配置每次启动随机密钥。UI API 校验 Host/Origin，不开启 CORS。日志为内存环形缓冲，最多 5000 行。引擎始终以 `--headless` 运行，界面由原生 WinUI 3 前端负责。

## 功能与待办

- P0：配置导入/增删改/启用、内核启动停止重启、原文错误、系统代理恢复、日志筛选/搜索/自动滚动、五页深色 UI、持久化。
- P1：每秒流量曲线/累计/连接数，连接单条及全部关闭，节点/分组及当前选中显示、单条/全部测速，订阅手动及自动更新，开机自启，剪贴板导入。
- P1.5：深链注册与导入、节点选择、单实例激活、双语与主题；原生托盘与后台常驻已在 v0.5.0 补齐。
- TODO P2：TUN/管理员权限/wintun.dll、规则编辑、内核更新检查。

本项目对接真实远程配置，内核启动及节点可用性仍依赖网络、订阅内容与 Tailscale 认证状态。不要把配置中的认证信息提交到 Git。

## P1.5 sing-box for Windows

Windows 使用当前用户命名互斥实现单实例；二次启动会通过 `runtime.json` 的 loopback 地址通知现有实例重新打开界面，深链参数也会转交。Linux 自检使用 flock，强杀不会留下阻塞锁。Settings 可一键注册/取消 `singboxwin://install-config?url=<encoded>`；Profiles 的「复制导入链接」使用输入框中的订阅 URL。导入链接含订阅地址，分享前请确认是否含凭据。

节点卡片列出当前配置的全部分组（`Selector` 以及 `URLTest` / `Fallback` / `LoadBalance` / `Relay` 等自动策略组）、成员、当前选择和延迟；运行时使用 Clash API 测速，停止时仍能读取配置分组。只有 `Selector` 组的成员行可以点击切换（`PUT /proxies/{group}`）；自动策略组的成员行是只读的，组头标明当前策略（如「URLTest 自动选择（最低延迟）」）与「只读」，当前生效节点按 `now` 字段高亮，点击不会向后端发请求，因此不再出现 `Clash API 400: Must be a Selector` 报错。

界面按 SFM 的侧栏与卡片层级整理：16px 线性图标导航、浅蓝选中态、28px 内容边距、16px 卡片间隔、18px 卡片内距、单一主操作、上下行双曲线、中英及深浅色切换。截图取自 Windows 交互会话里真实运行的原生客户端（`build/native/SingBoxWin.Native.exe`）：[仪表](docs/screenshots/native-01-overview.png)、[组](docs/screenshots/native-02-groups.png)、[组（自动策略组只读）](docs/screenshots/native-02b-groups-urltest.png)、[连接](docs/screenshots/native-03-connections.png)、[日志](docs/screenshots/native-04-logs.png)、[工具](docs/screenshots/native-05-tools.png)、[设置](docs/screenshots/native-06-settings.png)；另有[任务栏图标](docs/screenshots/native-07-taskbar-icon.png)、[程序图标](docs/screenshots/native-09-exe-icon.png) 与[图标各尺寸阶梯](docs/screenshots/native-10-icon-sizes.png)（16→256，浅色与深色任务栏各一行）。

本版采用纯 Go 后台常驻引擎 + 原生 WinUI 3 界面。v0.5.0 提供 Windows 原生托盘：点击打开窗口，右键可启停服务、切换系统代理和正常退出。默认点击关闭按钮隐藏到托盘，服务继续运行；设置中可改为关闭即退出。托盘不可用时保留窗口，避免无法找回。图标可能位于 Windows 隐藏图标区域。不增加 cgo、npm 依赖。

系统代理原值另写 `%APPDATA%\sing-box-win\proxy-backup.json`（先持久化后设置代理）；启动、停止或退出时，仅在 `ProxyEnable=1` 且 `ProxyServer` 与备份记录的本程序写入值一致时恢复，避免覆盖其他软件后续设置。强杀后下次启动同样恢复。默认混合端口统一为 **2085**。

### Engine mode (0.3.0)

The engine always runs headless and serves the HTTP API; the UI is the native
WinUI 3 app in `native/SingBoxWin.Native`. `--headless` (alias `--no-ui`) is
accepted for compatibility.
`--port 2086` fixes the API port; omit it for a random loopback port recorded in
`runtime.json`. Proxy settings still use their own port (default 2085).

- `GET /api/tools/info`: `version`, `core_version`, `mode`, `data_dir`, `log_path`.
- `GET /api/tools/tailscale`: `enabled`, `registered`, `state_dir_exists`,
  `hostname`, `ips` (without CIDR suffix), `key_expiry`, `last_sync` (RFC3339).
  Missing or invalid cache returns HTTP 200 with `registered:false`.
- `POST /api/tools/tailscale/test`: GET `http://100.68.38.67/` through the local
  mixed proxy, with a six-second timeout. Returns `ok`, `status`, `ms`, `error`;
  HTTP 200/301/302 succeeds. A stopped core returns `error:"core not running"`.

Core auto-connect continues to follow settings. Logs are also appended to
`engine.log` in the data directory. Windows builds must use
`-ldflags '-H windowsgui -s -w'` to prevent a console window (included in `make build`).
Checks require the build tags from the Makefile, including `with_gvisor`.

### v0.5.0 Windows 日常使用补齐

- 启动服务默认同时开启当前用户系统代理，停止/退出恢复原值。设置中的「启动服务时自动开启系统代理」可关闭此默认行为；不等于 TUN。
- 仪表增加系统代理状态和开关、Google 代理连通性检查；状态/连接、上传/下载以双列卡片展示。
- 应用设置增加自动连接、关闭到托盘、正常退出入口；登录自启启动原生客户端并隐藏到托盘。
- 配置卡片新增更新当前配置。更新运行中的配置会自动重新加载，失败则恢复原内容和服务。
- 验证记录和与本机 SFM 对照结果：[v0.5.0 验证](docs/v050-validation.md)。

### v0.5.1 配置管理

仪表顶部的配置卡片可直接编辑、更新、查看内容、导出、删除，或进入「管理全部配置」操作未启用的配置。

- 编辑名称、订阅 URL、每份配置的自动更新开关和分钟间隔；展示本地/远程类型和最后更新时间。
- 远程内容只读，可更改 URL 后更新；本地 JSON 可编辑，保存前校验。
- 更新正在运行的配置会重新加载并保留原系统代理状态；若启动失败，恢复之前的文件和服务。
- 删除当前配置前明确提示停止服务，停止后恢复原系统代理。删除前的内容保留在数据目录 profile-trash，更新前内容保留在 profile-history（均不上传）。
- 清空配置后仍可导入；不存在配置时禁用相关操作，本地配置禁用订阅更新。

详见 [v0.5.1 验证](docs/v051-validation.md)。二维码/深链分享和图形规则编辑未在本次实现。
