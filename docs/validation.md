# P0 验收记录（2026-11-10）

## 环境与构建

实际 Go 启动器 `/data/stacks/hermes-main/data/tools/go/bin/go` 为 1.24.5。用户指定的 `/opt/data/tools/go/bin/go` 在本次环境不存在。真实订阅有 `http_clients` 字段，1.12 原生解码不支持，因此采用 sing-box 1.14.4（最低 Go 1.25.5），构建工具链固定为 Go 1.26.10。没有使用 cgo/MSVC/Wails。

```text
export GOTOOLCHAIN=go1.26.10
export GOFLAGS='-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api'
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/singbox-win.exe ./cmd/singbox-win
# exit 0
-rwxrwxrwx 1 cuining cuining 82613760 Nov 10 00:44 build/singbox-win.exe
PE32+ executable (console) x86-64, for MS Windows

go vet ./...
# exit 0
GOOS=windows GOARCH=amd64 go vet ./...
# exit 0
```

Tailscale 实现在上游以 `with_gvisor` 标签保护。项目显式注册真实 endpoint 和 DNS 实现，不使用 Tailscale 占位实现。构建标签由 Makefile 提供，不依赖修改全局 Go 配置。

## 原生端点保真

按任务指定真实订阅下载到 `/tmp/sub.json`，未加入 Git。当前上游入口为 `option.Options.UnmarshalJSONContext`，并没有旧示例所说的包级 `option.UnmarshalJSON`。测试用该入口解析，再断言端点和解析后的路由规则。

```text
SINGBOX_TEST_SUB=/tmp/sub.json CGO_ENABLED=0 go test -v ./internal/config
=== RUN   TestEndpointFidelity
    config_test.go:37: len(opt.Endpoints)==1; type=tailscale; rule 100.64.0.0/10 exists=true
--- PASS: TestEndpointFidelity (0.09s)
PASS
```

## Windows 实跑

实际 PowerShell 路径 `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe`，interop 为 `/run/WSL/2_interop`。任务所述 `/host/...` 路径不存在。使用 `Start-Process C:\Users\giraf\projects\sing-box-win\build\singbox-win.exe` 启动，原始订阅保存于 `%APPDATA%\sing-box-win\profiles\test.json`，设置自动连接。

首次验收 PID 21484，loopback 地址 `http://127.0.0.1:57870`：

```text
WSL curl GET / HTTP 200
GET /api/state HTTP 200
{"error":"","profile":"test","running":true,"system_proxy":false,"tun":false,"version":"0.1.0"}
GET /api/traffic HTTP 200
{"connections":0,"download":0,"download_total":0,"upload":0,"upload_total":0}
GET /api/connections HTTP 200
profile import/rename/delete: PASS
node entries: 21
logs returned: 100
proxy enable: {"ok":true}; state system_proxy=true
proxy restore: {"ok":true}
core restart: {"ok":true}; state running=true
node delay HTTP 200 {"delay":269}
```

系统代理测试前后都为 `ProxyEnable=0`、`ProxyServer=127.0.0.1:<previous-proxy-port>`。没有停用或卸载 Karing、Tailscale、Clash 等软件。

存活时直接子进程为 1 个 `conhost.exe`，没有外置 sing-box 内核子进程。Edge 启动器没有保持存活。本次 WSL 启动客户端处于 Windows Session 0，已有 Edge 在 Session 1，因此**不能证明交互桌面上的可见窗口和关窗行为**。代码中 HTTP/内核生命周期不依赖浏览器进程，关闭浏览器不会触发退出；仍需在用户交互桌面双击 exe 补测实际窗口体验。

验收结束先调用 `/api/core/stop`，再按任务要求 `Stop-Process -Id 21484`：

```text
Stopped PID 21484
{"ProxyEnable":0,"ProxyServer":"127.0.0.1:<previous-proxy-port>"}
Remaining client processes=0
Remaining acceptance Edge launchers=0
```

强制结束进程不会运行清理代码。本实现将代理原值持久保存，下次启动恢复；日常应通过 UI「退出客户端」正常退出。TUN inbound 只从运行副本过滤，原始订阅、endpoint 和路由保留。

最终代码（增加浏览器启动错误日志后）再次以 PID 28220、端口 58008 实跑：`GET / HTTP 200`，`/api/state` 返回 `running:true`、`error:""`；先停止内核，再执行 `Stop-Process`。之后仅格式化静态 UI 文件并重新构建，最终产物大小见上文。
