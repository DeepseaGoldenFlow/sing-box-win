# CMP-42 启动性能与引擎日志（待 JARVIS 在 Windows 上验收）

本文只记录**改动依据**与**需要 JARVIS 执行的验证命令**。作者不在 Windows 会话内，未在本机声称构建、测试或冷启动结果通过；以下命令的输出才是验收依据。

## 改动依据（上游 sing-box v1.14.4 / SFM）

| 改动 | 依据 |
| --- | --- |
| `engine.log` 默认 `info`，仅诊断设置开到 `debug`/`trace` 才写更高等级 | 上游 `log.New` 把工厂级别设为 `LevelTrace`；`log/observable.go` 的 `output()` 对 `PlatformWriter` **不做级别过滤**（`observableLogger.Log` 只在没有 platform writer 且不需要 observable 时才短路）。SFW 就是那个 platform writer，所以级别门槛必须在 SFW 侧重新施加。`option.LogOptions.Level` + `log.ParseLevel` 是上游本身的写法。 |
| 5 MiB 轮转、保留 2 份（`engine.log` / `.1` / `.2`，合计 ≤ 15 MiB） | 上游没有日志轮转；SFM 用有上限的内存环（`docs/sfm-feature-spec.md`：`max_log_lines` 默认 300，seeded 3000）。Windows 端用「有上限的磁盘文件」实现同等约束，不引入 SFM 没有的行为。诊断设置仍由既有 Settings → 日志级别 控制。 |
| `startup-timing` 每个节点只写一次，且不计入轮转额度 | 上游只报一次自身耗时 `sing-box started (X.XXs)`（`box.go:507`），不报进程启动、配置解析、规则集就绪、API 监听；这些阶段由 SFW 自己记录。核心就绪时刻取 `box.Start` 返回点，此时所有规则集已在 `StartStateStart` 的 `task.Group`（`Concurrency(5)`、`FastFail`）里解析完（`route/router.go:139-156`）。 |
| 启用上游 `experimental.cache_file` 到数据目录 | 上游 `route/rule/rule_set_remote.go` 的 `StartContext` 顺序是 `cacheFile.LoadRuleSet` → `initial_path` → 同步 `fetch`；没有缓存时这个 fetch 发生在 Start 阶段且失败会中止启动。缓存文件只在调用方给出 `experimental.cache_file`（或 `PlatformLogWriter != nil`，`box.go:151-155`）时创建。SFW 只是把上游已有的缓存打开，不新增缓存语义。 |
| 自动连接改到后台（`startAsync`），`/api/state` 返回 `starting` | 上游远程规则集首次下载是**同步**的；不把它挪出请求路径，窗口与 HTTP API 都会被下载阻塞。SFM 的启动是进程内 `LibboxCommandClient` 推送状态，界面不等待核心；SFW 用「先起 API/窗口，核心后台起，`starting:true` 表示进行中」实现同一可见行为，不新增功能。 |
| 窗口先绘制第一帧，再开始探测/启动核心 | 原生端本来就在 `App.OnLaunched` 里 `window.Activate()` 之后才在 `OnFirstActivated` 里 `ConnectAsync`；本次再加 `Root.Loaded` 等待，保证「第一帧已呈现」早于引擎探测。属于需求 C 的排序约束，非新增产品功能。 |
| Settings 页日志级别下方增加说明文案 | 仅是既有 `Log Level` 控件的说明，解释它同时控制 `engine.log` 与 5 MiB 轮转；对应需求 A 的「仅诊断日志开启时升到 debug/trace」。 |

## 改动清单

- `cmd/singbox-win/logfile.go`（新）：`engine.log` 级别门槛 + 轮转；历史超大文件（例如 12.8 MB）在首次写入时立即轮转出去。
- `cmd/singbox-win/startup.go`（新）：`process-start` → `config-parsed` → `ruleset-local-ready` / `ruleset-remote-ready` → `core-started` → `api-listening` → `ui-first-frame`，每节点一条 `{"kind":"startup-timing",...}`，只写一次，不计入轮转。
- `cmd/singbox-win/rulesetcache.go`（新）：开启上游 `cache_file`，报告本地/远程规则集数量与缓存是否已存在。
- `cmd/singbox-win/main.go`：级别门槛、`WriteMessage` 改为带门槛的批量写入、`prepareOptions` / `newCore` / `installCore` / `startAsync` 拆分、自动连接不再阻塞 API、`/api/state` 增加 `starting`、`stop()` 取消进行中的后台启动。
- `cmd/singbox-win/pages.go`：`/api/native/startup`、`/api/native/ui-ready`、`/api/native/status` 增加 `log_files` 与 `startup`、`/api/native/core` PUT 立即更新文件门槛。
- `native/SingBoxWin.Native/*`：首帧时刻上报（`at_unix_ms`/`elapsed_ms`）、`starting` 映射为「启动中」、Settings 诊断日志说明。
- 测试：`logfile_test.go`、`startup_test.go`。
- **未改动 `scripts/build-native.ps1`。**

## JARVIS 需执行的验证命令（Windows）

在仓库根目录、已安装 Go 1.26.10 的 Windows 环境执行。构建标签必须与 Makefile 一致。

```powershell
$env:CGO_ENABLED = "0"
$env:GOTOOLCHAIN = "go1.26.10"
$env:GOFLAGS = "-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api"

go build -tags with_gvisor,with_quic,with_utls,with_clash_api ./...
go test  -tags with_gvisor,with_quic,with_utls,with_clash_api -p 1 ./cmd/... ./internal/...
```

`-p 1` 不能省：测试会占用本机端口，并发跑会互相冲突。

### A. 日志级别与轮转

```powershell
# 1) 默认级别：把日志级别留空/Info，启动内核跑一段，engine.log 不应出现 debug/trace 行
$dir = Join-Path $env:APPDATA "sing-box-win"
Select-String -Path "$dir\engine.log" -Pattern '"level":"(debug|trace)"' | Measure-Object

# 2) 诊断日志：设置页把「日志级别」改为 Debug 后重启内核，应出现 debug 行
# 3) 文件规模：三个文件都存在时合计不得超过 15 MiB
Get-ChildItem "$dir\engine.log*" | Select-Object Name, Length
(Get-ChildItem "$dir\engine.log*" | Measure-Object Length -Sum).Sum / 1MB

# 4) 历史 12.8 MB 文件：保留旧文件后启动一次，engine.log 应被轮转为 engine.log.1
```

期望：`engine.log` 默认只有 `info`/`warn`/`error`；诊断关闭时**没有** `debug`/`trace`；单文件 ≤ 5 MiB；最多 3 个文件；`startup-timing` 行存在但不会因为自身触发轮转。

### B. 启动阶段计时

```powershell
$dir = Join-Path $env:APPDATA "sing-box-win"
Select-String -Path "$dir\engine.log" -Pattern '"kind":"startup-timing"'
Invoke-RestMethod (Get-Content "$dir\runtime.json" | ConvertFrom-Json).url + "/api/native/startup"
```

期望：每个节点只出现一次，`ms` 单调不减，顺序为 `process-start` < `config-parsed` < `ruleset-*` ≤ `core-started` < `api-listening`（`ui-first-frame` 由窗口上报，可与核心先后无关）。`ruleset-local-ready` 与 `ruleset-remote-ready` 分别带 `count`，远程还带 `cache_file` / `cache_present` / `tags`。

### C. 窗口不被阻塞

```powershell
# 冷启动（先删除 runtime.json，模拟首启）
Remove-Item (Join-Path $env:APPDATA "sing-box-win\runtime.json") -ErrorAction SilentlyContinue
# 启动 build/native/SingBoxWin.Native.exe，观察窗口出现时刻
# 窗口出现后立即请求：
Invoke-RestMethod (Get-Content "$env:APPDATA\sing-box-win\runtime.json" | ConvertFrom-Json).url + "/api/native/ui-ready" -Method Post -ContentType "application/json" -Body '{"elapsed_ms":1}'
```

期望：窗口先出现，随后 `/api/state` 显示 `starting:true`，核心起来后变 `running:true`；`engine.log` 的 `ui-first-frame` 记录（含 `unix_ms`、`client_elapsed_ms`）早于或独立于 `core-started`。**用截图或录屏佐证「窗口出现」早于「内核 started」**，这是需求 C 要求的证据。

### 冷启动对比

同一网络、同一配置，冷启动 3 次，记录「双击到窗口出现」与「窗口出现到 `/api/state` running」。对照 JARVIS 原测基线：纯内核首启 27.65 s、Defender 排除后新文件 8.81 s、重复运行 4.13 s / 1.22 s，以及内核日志自报 `sing-box started (1.04s)`。本次改动**不改变**进程启动前的系统/Defender 时间，只把可归因于 SFW 的阶段完整计时，并把规则集下载移出首帧路径。
