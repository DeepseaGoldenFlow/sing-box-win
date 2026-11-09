# sing-box for Windows

纯 Go 单进程客户端：内嵌静态 Web UI，HTTP 仅监听 `127.0.0.1` 随机端口，启动 Edge 应用窗口；关闭窗口后后端继续运行。在 UI 点击「退出客户端」可停止内核、恢复代理并退出。

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

配置通过 sing-box `option.Options.UnmarshalJSONContext`（当前版本的原生 JSON 解析入口）解析，注册真实 Tailscale endpoint/DNS 实现；不转换成自定义 JSON 模型。运行副本保留 endpoint、路由和 outbound，过滤 TUN inbound（本期未提供管理员权限/wintun 支持），关闭配置中的 `set_system_proxy`，监听地址遵循客户端允许局域网设置，并增加客户端专用 mixed 入口（默认 2085）。原订阅不改写。

系统代理仅写当前用户 Internet Settings 的 `ProxyEnable`、`ProxyServer`，使用 WinINet 广播刷新；原值持久保存，在关闭代理、正常停止、退出或下次启动时恢复。强制结束进程无法执行即时恢复；下次启动会恢复。已有其他代理软件可能同时写这些值，建议避免同时切换系统代理。

Clash 控制器另开随机 loopback 端口并配置每次启动随机密钥。UI API 校验 Host/Origin，不开启 CORS。日志为内存环形缓冲，最多 5000 行。`SINGBOX_NO_BROWSER=1` 可供无人值守验证，不启动浏览器。

## 功能与待办

- P0：配置导入/增删改/启用、内核启动停止重启、原文错误、系统代理恢复、日志筛选/搜索/自动滚动、五页深色 UI、持久化。
- P1：每秒流量曲线/累计/连接数，连接单条及全部关闭，节点/分组及当前选中显示、单条/全部测速，订阅手动及自动更新，开机自启，剪贴板导入。
- TODO P1：深链注册与导入、节点选择交互、托盘、原生单实例激活。
- TODO P2：TUN/管理员权限/wintun.dll、多语言、规则编辑、内核更新检查。

本项目对接真实远程配置，内核启动及节点可用性仍依赖网络、订阅内容与 Tailscale 认证状态。不要把配置中的认证信息提交到 Git。
