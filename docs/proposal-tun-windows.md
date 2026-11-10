# Windows TUN 全流量接管与高权限运行方案

> CMP-38 设计稿。基线：`codex/windows-tray-and-proxy @ 239fd9f518acb44ead0057dda319e89626bb56b1`（v0.6.0）。本稿只设计，不改产品代码，不宣称 Windows 实机验收或构建通过。Go/VS 编译、服务安装和网络故障注入由 JARVIS 在**隔离测试机/测试配置**上执行。
>
> 当前不能把“保留 TUN 配置”或 HTTP 204 当作 TUN 已实现：引擎明确丢弃 TUN inbound，状态固定为 `tun:false`（`cmd/singbox-win/main.go:157-180,385-391`）；现有连通性测试显式走本地 HTTP 代理（`cmd/singbox-win/tools.go:111-138`）。注册表包含 TUN registry 并不等于已经创建 TUN（`internal/config/config.go:13-19`）。

## 0. 决策、范围与证据口径

**推荐：普通权限 WinUI/每用户代理 + 管理员批准安装、按需启动的最小权限 Windows 服务（TUN broker）。** WinUI 和当前每用户 Go engine 不整体提权；服务仅接受固定版本的启动/停止/重载/恢复请求，驱动与 sing-tun 在受保护的专用 engine-host 中运行。后续实现须把“TUN 所需最小 host”与配置管理 API 分离，而不是简单给现有 `sing-box-win.exe` 加 `runas`。

| 方案 | 结论 | 原因 |
|---|---|---|
| 每次 UAC `RunAs` 整个客户端 | 否决 | 持续弹窗，错误地提高 UI、配置读取/订阅获取/深链处理权限；换用户和异常恢复都困难。仅安装/修复动作可触发 UAC。 |
| 管理员目录中的 helper，用户按需启动 | 部分采用 | `Program Files` ACL 能保护二进制，但普通用户不能直接取得服务级网络管理权限；它应由服务控制管理器启动，并有受控 IPC。 |
| Windows 服务 + 专用 TUN engine-host | **采用** | 开机无需用户凭据；可恢复异常残留；天然跨会话。必须收窄命令面、保护二进制和 ACL，不能照搬当前 engine API。 |
| 最高权限计划任务 | 否决 | 容易被当作持久化提权入口，会话/电源/登录语义不适配 TUN 生命周期。**禁止将 Desktop、Downloads、repo/build、用户可替换的 exe 注册为最高权限常驻任务。** |

概念对应 SFM 的受控 daemon（`Library/Network/HelperServiceManager.swift:9-27`）与系统 PacketTunnel 生命周期（`Library/Network/ExtensionProfile.swift:152-165,237-322`）；Windows 不复制 Apple Network Extension 文案或权限模型。SFM 引用固定于 `SagerNet/sing-box-for-apple@768b3de2baf2461dd06405cba1b2ca4e39da5ea4`（`dev` 读取时）；上游引用固定于本仓库 `go.mod` 的版本。本文 `sing-tun/...`、`sing-box/...` 分别指这两个依赖源码。

### 0.1 进程与权限边界

| 进程 | 身份/目录 | 允许负责 |
|---|---|---|
| WinUI 3 | 交互用户；用户应用目录；不添加 `requireAdministrator` | 展示、编辑草稿、请求启停；无权直接改全局路由/WFP |
| 每用户 engine/agent | 同一交互用户 SID/会话；`%LOCALAPPDATA%\SFW` | 订阅、profile、用户设置、本地端口、**自己的 HKCU/WinINET 代理**、备份与 UI 状态 |
| TUN broker 服务 | 优先专用低权限虚拟服务账户，只授予实测需要的网络/驱动权限；不足才用 LocalSystem | 验证调用者、签发/回收 session、拉起/停止 host、持久化恢复日志 |
| 专用 TUN engine-host | 服务创建的隔离会话；`C:\Program Files\SFW\Core` | sing-box/sing-tun、wintun adapter、路由/DNS/WFP、host 健康状态；不开放当前通用配置/订阅/深链 API |

- 第一阶段不能宣称已找到低于 LocalSystem 的完整权限集合。以 `SeLoadDriverPrivilege`、实际 token 和 wintun/NETIO/WFP 操作结果判定；若为兼容 wintun 先选 LocalSystem，也只能运行上面的窄 host，不能把订阅、profile 或任意路径参数带进去。
- `Program Files\SFW` 由安装器创建；二进制/配置模板目录只允许 `SYSTEM`、`Administrators`、受信安装服务写入，用户仅读取执行。服务 binary path 用引号，禁止 Desktop、Downloads、可写 `build/native`、repo 路径成为服务 executable。
- 安装、升级、修复、卸载均要求管理员批准，校验发布者签名、包版本和 hash；升级先停止 TUN 并恢复网络，再原子替换，失败回滚。普通用户不能借“更新路径”替换高权限 exe。
- 服务默认 **DEMAND_START**，不是开机常驻高权限 UI。只有 TUN 开启时 host 存活；普通 system-proxy 模式不需要提权。停止时不依赖启动媒介仍在场。
- 服务控制走 ACL 受控的 named pipe（可选 ALPC），不用所有进程可猜测的 localhost 管理端口。pipe 默认允许交互用户枚举/连接，但每个请求必须再经过命名 pipe SID 模拟校验、Bearer 和 session/generation 校验；连接成功不等于有权限。
- 当前单实例 mutex `Local\sing-box-win-<USERNAME>` 与 WinUI 的 `Global\SingBoxWinNative*` 不是认证（`internal/platform/instance_windows.go:10-23`、`native/SingBoxWin.Native/App.xaml.cs:22-32`）。保留单实例语义，另建带 DACL 的每用户同步对象/IPC，拒绝其他 SID/会话代替当前 UI 激活。
- 用户 engine 先校验完整 profile 原文，再以有大小上限、版本/hash、固定 request schema 的 `StartTun` 交给服务；**broker 不信任用户侧校验，必须独立再解析并执行字段白名单**：privileged host 只允许一个 TUN inbound，不允许其他本地监听 inbound、实验性外部控制器、任意文件读写/证书路径或未经批准的 endpoint；outbound/route/DNS 仅保留实现代理转发需要的受支持字段。broker 不接收任意 HTTP URL、任意文件路径、命令行、可执行路径或任意 PowerShell/netsh 字符串，profile 只在受保护进程内使用、不写 journal。host 不接受远程/浏览器管理 API，内部 Clash 端口保持 loopback 且继续使用独立 secret。

### 0.2 Bearer、runtime ACL、二次实例激活

这是提升任何现有后台能力之前的硬门槛。当前 runtime 只有 URL/PID（`cmd/singbox-win/main.go:662-668`）；Host/Origin 检查不是认证（同文件 `674-686`）；Native 首次探测和后续请求都没有 Authorization（`native/SingBoxWin.Native/EngineClient.cs:12-48`）；Go 二次激活 POST 也无 Authorization（`cmd/singbox-win/client.go:52-87`）。`/api/state` 也必须保护，因为 URL/PID 泄露不能换来状态或写操作。

1. 每用户 engine 每次启动用操作系统 CSPRNG 生成至少 32 字节随机 token（base64url，不带 padding）。不要复用 Clash secret、PID、用户名、时间戳或安装 ID。进程创建/重启均轮换；异常退出后旧 token 自然失效。token 不写日志、错误文本、崩溃报告、命令行、URL/query、遥测、profile、repo 或测试报告。
2. 明文 secret 只放 `%LOCALAPPDATA%\SFW\run\<instance-id>\engine.token`，与可公开给同用户客户端的 `runtime.json` 分离。目录和文件使用 NTFS DACL：仅该交互用户 SID 读/写，`SYSTEM` 为服务恢复所需可访问；禁用继承并显式去除 `Everyone`、`Users`、`Administrators` 的非必要继承授权。管理员/token 是否可读由 OS 权限决定，不能声称能抵御本机管理员。
3. `runtime.json` 至少含 `url,pid,process_start_time,instance_id,user_sid,session_id,protocol_version`；secret 文件权限失败、文件不是普通文件、链接/重解析点或属主不符时拒绝启动/连接。连接端检查 PID 的启动时间和 instance，不能只信一个可复用 PID。临时文件写盘、flush、原子 rename 均受 ACL 保护。
4. 每个 `/api/*`（包括 `/api/state`、core stop、quit）及 `/api/activate` 都先验证 `Authorization: Bearer <token>`，再处理请求；缺失/错误/空/多余 scheme 均返回无敏感信息的 **401**，错误/正确 token 比较用常量时间。现有 Clash `Bearer`（`cmd/singbox-win/main.go:181-195,300-322`）只保护内部 Clash，不替代 Engine token。
5. `EngineClient` 的首次 runtime 探测、重连、全部读/写调用统一注入 Authorization；`activateExisting` 同样注入。深链/协议激活先校验每用户 SID/session、instance 和 token，且普通“显示主窗口”不导入 URL。导入订阅仍走显式 UI 确认/校验，不能因认证通过就允许任意外部链接自动改配置。
6. broker token 与上述用户 engine token 分离。broker 每次启动生成服务 token，secret 只允许服务账户/SYSTEM 读取；用户 engine 通过经 SID 模拟验证的 pipe 获取**绑定 user SID、session、instance、到期时间**的短期 capability。服务请求绑定 generation，重放旧启动/停止不得影响新会话。跨用户/会话一律拒绝。
7. 管理员/SYSTEM 发起的显式恢复操作走单独、最小的 `recover` 命令和审计日志，不能借用用户 engine token 获得订阅、profile 等权限。若未来采用 Windows Hello/DPAPI 保护 secret，要明确 DPAPI 不替代进程 ACL 和请求认证；首版不做无必要的复杂密钥体系。

### 0.3 HKCU 系统代理的唯一属主

现有代理操作在调用进程自己的 `HKEY_CURRENT_USER`（`internal/platform/platform_windows.go:20-66`），并以 `InternetSetOptionW` 通知 WinINET（`58-64`）；恢复逻辑见 `cmd/singbox-win/main.go:117-134`、`client.go:24-50`。因此：

- 继续由**交互会话中的每用户 agent**读取/修改 HKCU、通知 WinINET，保留用户 `system_proxy` 偏好。TUN 开关不偷偷开启 system proxy；两项独立验收。
- **LocalSystem 不得写自己的 HKCU，也不得加载用户 hive 冒充正常用户设置。** broker 返回成功给 agent，agent 才按原逻辑变更；失败不切换到半完成状态。
- 每用户备份含原 `ProxyEnable/ProxyServer/ProxyOverride/AutoConfigURL`、用户 SID、agent instance、SFW 写入值。停止/崩溃后仅当前值仍与 SFW 写入值相符才恢复；用户后来改动则不覆盖，并报告需人工确认。
- 多用户同时登录时，各自 agent 只处理自己的 hive；broker session 单独记账，引用计数之外还要判断依赖关系，用户 A 的停止不能拆掉用户 B 的 TUN。首版可明确“一台机器只允许一个活动 TUN owner”，第二用户必须收到冲突而不是接管。

## 1. wintun：采用内嵌，不引入 DLL 下载/系统依赖

已核对当前 `go.mod`：sing-box `v1.14.4`、sing-tun `v0.9.7-0.20261023115432-040db71fcd33`、`golang.zx2c4.com/wintun`（`go.mod:19,137,176`）。

- sing-tun 用 `//go:embed amd64/wintun.dll` 嵌入 DLL 字节（`sing-tun/internal/wintun/dll_windows_amd64.go:8-13`），再 `memmod.LoadLibrary(dllContent)` 从内存加载（`sing-tun/internal/wintun/dll_windows.go:76-93`）。**不需要也不应运行时下载 wintun.dll，不要求应用目录/System32 存在 DLL。**
- 驱动由 Wintun API 创建/打开 adapter；正常停止关闭 session/adapter（`sing-tun/internal/wintun/wintun_windows.go:22-30,41-91`、`sing-tun/tun_windows.go:51-83,619-642`）。正常 stop 禁止全局 `WintunDeleteDriver`，否则可能破坏其他 wintun 客户端。
- `include.InboundRegistry` 和 `tun.RegisterInbound` 已链接（`sing-box/include/registry.go:52-78`）；现代 `address/route_address/route_exclude_address` 会映射到 sing-tun（`sing-box/protocol/tun/inbound.go:63-101,198-235`）。实现时是移除当前过滤并保证 sing-box 生命周期能关闭 TUN（同文件 `441-452,465-499`），不是自己另写一套 TUN 数据面。
- 保持仓库既定 `CGO_ENABLED=0`、Go/toolchain/build tags（`Makefile:1-12`）。版本/依赖升级另开任务，不在 CMP-38 顺手升级。
- 仍需实机核验：Windows 版本/架构、wintun 驱动安装权限、第三方安全软件、adapter 创建和版本。DLL 内嵌不等于驱动已安装，也不等于服务 token 合适。

JARVIS 在源码目录、隔离构建机上执行（只读取模块；构建不启动 TUN）：

```powershell
Set-Location C:\work\sing-box-win   # 改为 JARVIS 实际 checkout
$env:GOTOOLCHAIN='go1.26.10'; $env:CGO_ENABLED='0'
$env:GOFLAGS='-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api'
go version; go env GOOS GOARCH CGO_ENABLED GOTOOLCHAIN
go mod download github.com/sagernet/sing-tun
$tun = go env GOMODCACHE + '\github.com\sagernet\sing-tun@v0.9.7-0.20261023115432-040db71fcd33'
Select-String "$tun\internal\wintun\dll_windows_amd64.go" 'go:embed'
Select-String "$tun\internal\wintun\dll_windows.go" 'memmod.LoadLibrary'
go list -m -json all | Set-Content "$env:TEMP\sfw-modules.json"
make build
# 无 make 时使用同一 toolchain/flags：
go build -ldflags '-H windowsgui -s -w' -o build\sing-box-win.exe ./cmd/singbox-win
Get-FileHash build\sing-box-win.exe -Algorithm SHA256
```

**通过：**版本完全匹配；embed/memory-load 证据可见；只出现目标 exe，发布目录不存在下载的 `wintun.dll`。然后在管理员批准的测试服务上启动最小 TUN：

```powershell
Get-CimInstance Win32_PnPSignedDriver | Where-Object DeviceName -Match 'Wintun' |
  Select-Object DeviceName,DriverVersion,DriverProviderName,InfName
Get-NetAdapter -IncludeHidden | Where-Object {$_.InterfaceDescription -match 'Wintun'}
Get-AuthenticodeSignature C:\Program Files\SFW\Core\sing-box-win-tun.exe
Get-FileHash C:\Program Files\SFW\Core\sing-box-win-tun.exe -Algorithm SHA256
```

**通过：**创建本 session 预期的 adapter，状态 Up，签名/哈希与已批准构建一致，无外部 DLL 搜索/下载失败。**不得**以 `where.exe wintun.dll` 找不到为失败；不得手工复制 DLL 到 System32 来“修复”。

## 2. 路由、DNS、排除和共存

### 2.1 最小生成配置与 DNS 语义

首个用户配置没有 TUN 时，agent 生成受版本控制的默认 TUN block；已有 TUN 时保留地址、MTU、stack、路由和 endpoint，不静默覆盖。建议默认 `tag=tun-in`、`interface_name=SFW`、`address=172.18.0.1/30`（若与本机任何 prefix 冲突则选另一私网 /30，并展示）、`mtu=1500`、`stack=system`、`auto_route=true`、`strict_route=true`。默认 `dns_mode` 为空且 `auto_route=true` 时是 hijack；TUN DNS 地址默认为首个地址的下一个地址，例如 `172.18.0.1/30 -> 172.18.0.2`（`sing-tun/tun.go:128-180`）。

- profile 必须含明确 DNS server/rule 和出口策略，禁止“创建了 adapter 但 DNS 无可用上游”的假成功。DNS hijack 在 sing-box TUN 中接实际路由（`sing-box/protocol/tun/inbound.go:544-565`）。
- `system` stack 有真实 packet forwarding/UDP NAT，不是 HTTP proxy（`sing-tun/stack_system.go:69-114,135-205`）；最终以连接日志的 `inbound=tun-in` 和抓包确认。
- Windows 默认 auto-route 为 `0.0.0.0/0`（IPv6 为 `::/0`），不是 macOS 的 subranges（`sing-tun/tun_rules.go:107-143,161-190`）。`route_exclude_address` 由 IPSetBuilder 做前缀扣除（`144-159,194-209`）；SFW 只生成/合并排除并交给 sing-tun，不并发跑第二套 `route add`/`netsh` 路由引擎。
- 自动生成的排除列表保存在用户 profile 的明确 `sfw_managed_exclusions` 元数据/生成层，和作者已有的 `route_exclude_address` 求并集，不删除作者配置。在每次启动/换网重新计算，UI 可查看/编辑 LAN/NAS/WSL 的选择。
- 保留 sing-box 的默认网卡 monitor 和 outbound interface binding（`sing-tun/monitor_windows.go:39-79,157-222`、`sing-box/common/dialer/default.go:77-131`、`sing-box/route/network.go:340-365`），避免代理出口递归进入自己的 TUN。禁止设置全局进程路由 hack 代替它。
- `strict_route` 使用 WFP 动态生命周期；关闭时清理（`sing-tun/tun_windows.go:213-433,627-629`）。普通运行不创建永久 WFP filter/固定 persistent provider；清理只针对 SFW session 所有对象。
- IPv4、IPv6 分开验收。若当前 profile 未配置 IPv6 地址/路由，不宣称 IPv6 接管；要么明确禁用并解释，要么添加 IPv6 ULA/默认路由且通过测试，不能留下绕过开关的 IPv6 漏流。

### 2.2 具体排除策略

启动前快照实际接口、地址、路由、DNS、默认物理出口和 NAS 目标；只有用户明确选择的范围进入 `route_exclude_address`。`Recover`、安装/升级/卸载等系统管理动作才要求管理员批准，普通 LAN/NAS 排除不需要把每项选择变成 UAC 弹窗：

| 场景 | 策略 | 禁止/注意 |
|---|---|---|
| LAN | 默认排除当前活动物理接口的 connected prefix，例如实测 `192.168.50.0/24`；保留已有更具体路由 | 不一刀切排除 `10/8,172.16/12,192.168/16`，否则公司私网/VPN 会绕过策略 |
| NAS | 用户确认 NAS 位于 LAN 时复用该精确 prefix；NAS 跨网段则只添加 NAS peer `/32` 或实际 LAN 路由 prefix | 不把 NAS 域名解析结果不经验证全部排除；SMB 用实际地址测试 |
| WSL2 | 识别 `vEthernet (WSL...)`/Hyper-V 接口的 connected prefix，例如实测 `172.25.16.0/20`，默认排除该 prefix | WSL 网段每次启动可变，禁止写死示例；Hyper-V 其他 VM 网段不能仅凭“虚拟”全排除 |
| 独立 Windows Tailscale | 存在真实 Tailscale adapter/service 时排除 `100.64.0.0/10`；保留其 interface route、MagicDNS 策略，并验证 netcheck/peer | 不能仅凭安装了 Tailscale 就删改其路由；不全局关闭 strict_route 来逃避冲突 |
| 内嵌 sing-box `ts-ep` | 默认 netstack 模式不把 `100.64.0.0/10` 无条件排除：tailnet 流量需进入 sing-box，交给 Tailscale endpoint，socket 再由其 interface control 出物理口 | 必须检测 `endpoints[]` 实际 type/config。`system_interface=true` 会创建第二个 TUN（`sing-box/protocol/tailscale/endpoint.go:325-368`），首版明确不支持的组合则阻止并提示，不能双默认路由抢占 |
| 其他 VPN | 发现默认路由/TUN/WFP 竞争即显示冲突；先要求用户明确优先级或不做全接管 | 不删除、禁用其他 VPN 的 adapter、DNS、WFP 或路由 |

内嵌 Tailscale 的 UDP 出站与 interface control 已有实现（`sing-box/protocol/tailscale/endpoint.go:398-435`），当前 UI 的实时状态/退出节点也是操作真实 endpoint（`cmd/singbox-win/live_endpoints.go:34-58,117-149`）。所以两种 Tailscale 是不同测试矩阵，不可用“100.64/10 已排除”一项冒充全部兼容。

### 2.3 正常停止、异常强杀、重启与换网

**正常 stop：**

1. agent 停止接收新启动请求，标记 session `stopping`；broker 只停止该 generation。
2. sing-box lifecycle 关闭 TUN；sing-tun 结束 session、关闭 adapter/WFP、注销 listener、恢复 weak-host、flush DNS（`sing-tun/tun_windows.go:619-642`）。agent 再按 0.3 恢复自己的代理。
3. 对账启动前快照：SFW adapter/session 不存在，SFW route/WFP 无残留，DNS/默认路由与原值一致，weak-host 只恢复本次改过的项。清理完成才返回 success 和更新 `tun:false`。
4. 任何一步失败都不报“已恢复”：保留 journal、显示实际残留和可执行的 `Recover` 入口。

**崩溃/强杀：** 死进程无法执行 Go `Close`，不能假设 stop hook 会运行，也不能声称 watchdog 能替它执行。服务基于持久化 journal 和系统实况做补偿。

journal 位于管理员/服务可写的 `%ProgramData%\SFW\recovery`，普通用户只读不可改；每项原子写入并包含：

- boot ID、user SID、session/generation、host PID + process start time、host build/hash；
- SFW adapter GUID/LUID/name、session ID、生成配置 hash、原默认接口；
- **每个** 修改过的 route/DNS/WFP/weak-host 的接口 LUID、IPv4/IPv6 family、原值、新值、SFW ownership（route 需区分地址族/前缀/接口/网关/metric，WFP 需记录本次动态 session/provider/filter/callout key，不能只写“存在 TUN”）；
- 当前状态和最后心跳。凭据、profile、订阅 URL 不写 journal。

sing-tun 为保证出口 interface binding 会关闭其他接口的 weak-host send，并只记录本进程内存中关闭过的项（`sing-tun/tun_bind_windows.go:35-104`）。实现必须在**每次修改前**持久化 `prepared + 原值/预期新值`，flush 后才修改，成功后再记录 `applied`；若强杀落在两者之间，恢复时仅在接口 identity/family 匹配且当前值等于预期新值时撤销，当前值仍等于原值则视为未改。仅记录“修改成功”会留下漏记窗口。每个用户 proxy 的 journal 留在其 agent，由新 agent 用 SID/instance/当前值所有权校验恢复；服务不代写 HKCU。

broker 下次启动、用户显式 `Recover` 以及健康检查发现 host 消失时：

1. 先确认 boot/generation、adapter GUID/LUID 和进程启动时间，拒绝把 recycled PID 或别的应用 adapter 当成自己的。
2. 只清理与 journal 匹配的 SFW adapter/route/WFP/session；不存在视为已清理，不报错。其他应用同名、同驱动、同网段但 ownership 不匹配的一律不动。
3. 只在 weak-host 当前值仍是 journal 记录的 SFW 写入值时恢复原值；恢复 DNS 只针对 SFW 写过的接口/值。不得全网卡启用 weak-host、重置 TCP/IP/Winsock、清光路由/DNS。
4. flush DNS，重读实况并和 baseline 对账；有未知残留则失败可见、保留证据。只有全部对账后才删除/归档 journal。恢复命令不自动启动新 TUN。
5. host 被强杀时 broker 不应立刻反复拉起；先恢复，再由用户启动。服务自身被强杀则重启后读取 journal，不能靠心跳内存恢复。

休眠/唤醒、默认网卡变化、WSL 重启、Tailscale 上线/下线，先暂停新连接并重新计算 exclusions/default interface；安全重载失败就停止并恢复，不保留半套路由。SFM 的排除覆盖与重载语义参考 `ApplicationLibrary/Views/Setting/ProfileOverrideView.swift:23-39` 和 `Library/Network/ExtensionPlatformInterface.swift:90-111`。

## 3. JARVIS 通用准备与测试夹具

下列是**给 JARVIS 的命令，不是本设计稿的执行记录**。所有 `<...>` 必须替换为测试机实际值；示例 IP 不得直接当验收数据。每个阶段输出 `commands.txt`、stdout/stderr、JSON 状态、路由/DNS 快照和对应 commit hash，密钥必须脱敏。

在管理员 PowerShell 记录 build/服务 token；另开**非管理员** PowerShell 模拟正常 UI/agent。先确认是在测试机，且没有其他正在使用的 SFW/TUN：

```powershell
Set-Location C:\work\sing-box-win
git rev-parse HEAD; git status --short
whoami /user; whoami /groups; whoami /priv
Get-Process SingBoxWin.Native,sing-box-win,sing-box-win-tun -ErrorAction SilentlyContinue |
  Select-Object Id,ProcessName,Path,StartTime,SessionId
Get-CimInstance Win32_Service -Filter "Name='SFWTun'" |
  Select-Object Name,State,StartMode,StartName,PathName
```

实际进程 token 还要用测试程序/诊断命令 dump `TokenElevationType`、`TokenElevation`、`TokenIntegrityLevel`、`TokenUser`、`TokenSessionId`；`IsInRole(Administrator)` 或 `whoami /priv` 单独都不够。期望：UI/agent 为交互用户、普通 IL、非 elevated；broker 为批准的服务 SID/session 0；其受控子进程权限不高于设计。实现阶段把此诊断输出加到 `scripts/test-windows-service-security.ps1`，由 JARVIS 执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-windows-service-security.ps1 -ResultPath "$env:TEMP\sfw-security.json"
Get-Content "$env:TEMP\sfw-security.json"
```

**通过：**UI/agent 的 elevated 检查为 false、integrity 为 Medium，非管理员测试用户的 `TokenElevationType=Limited`、管理员用户未提升的进程为 `Default`；服务路径无用户写权限、pipe 请求者 SID/session 正确、跨用户请求拒绝；不是“任务管理器里显示管理员”即通过。

每阶段前后保存基线（参数由实现脚本提供；不得在已有真实配置上直接运行）：

```powershell
$Evidence = "$env:TEMP\sfw-tun-evidence\P1" # 各阶段改目录
New-Item $Evidence -ItemType Directory -Force
Get-NetAdapter -IncludeHidden | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\adapters.json"
Get-NetIPAddress | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\addresses.json"
Get-NetRoute -AddressFamily IPv4,IPv6 -ErrorAction Stop |
  Sort-Object InterfaceIndex,DestinationPrefix,RouteMetric |
  ConvertTo-Json -Depth 5 | Set-Content "$Evidence\routes.json"
Get-DnsClientServerAddress | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\dns.json"
Get-DnsClientGlobalSetting | ConvertTo-Json -Depth 5 | Set-Content "$Evidence\dns-global.json"
Get-NetIPInterface | Select-Object InterfaceIndex,InterfaceAlias,AddressFamily,Forwarding,WeakHostSend,WeakHostReceive,ConnectionState |
  ConvertTo-Json -Depth 5 | Set-Content "$Evidence\ip-interfaces.json"
route print | Set-Content "$Evidence\route-print.txt"
Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' |
  Select-Object ProxyEnable,ProxyServer,ProxyOverride,AutoConfigURL |
  ConvertTo-Json | Set-Content "$Evidence\wininet.json"
```

脚本应将输入地址写成机器局部变量，**不要上传真实订阅或私网拓扑**：

```powershell
$LanPrefix   = '<活动物理网卡 connected prefix，如 192.168.50.0/24>'
$LanIp       = '<同网段独立 TCP 测试服务器 IP>'
$NasIp       = '<实际 NAS IP>'
$WslPrefix   = '<Get-NetIPAddress 查到的 WSL connected prefix>'
$WslIp       = '<wsl hostname -I 的当前 IPv4；不要取 127.0.0.1>'
$TailscaleIp = '<独立 Tailscale 对端的 100.x 地址；tailscale status 获取>'
$TunTcpIp    = '<Internet TCP echo/fixture IP>'; $TunTcpPort = 47001
$TunUdpIp    = '<Internet UDP echo/fixture IP>'; $TunUdpPort = 47002
$DnsName     = '<每次带唯一前缀且由测试 DNS 记录的域名>'
$TestRoot    = '<隔离 fixture 根目录；不得是现有用户 profile>'
```

`LanIp/TunTcpIp/TunUdpIp` 不得是应用服务器本身、默认网关或 TUN 自己。公网 fixture 必须经授权且记录 TCP/UDP 请求 ID/时间，Windows 测端只建立**原始 socket**，清掉环境代理，不读取 WinINET、不调用现有 `/api/tools/connectivity`。未来实现提交 `scripts/test-tun-traffic.ps1` 和最小 TCP/UDP echo fixture；以下是每次必须独立执行的接口：

```powershell
$env:HTTP_PROXY=''; $env:HTTPS_PROXY=''; $env:ALL_PROXY=''; $env:NO_PROXY=''
powershell -NoProfile -File .\scripts\test-tun-traffic.ps1 -Mode Tcp -Server $TunTcpIp -Port $TunTcpPort -RequestId 'p3-tcp-001' -ResultPath "$Evidence\tcp.json"
powershell -NoProfile -File .\scripts\test-tun-traffic.ps1 -Mode Udp -Server $TunUdpIp -Port $TunUdpPort -RequestId 'p3-udp-001' -ResultPath "$Evidence\udp.json"
Clear-DnsClientCache
Resolve-DnsName $DnsName -Type A -DnsOnly -NoHostsFile | Tee-Object "$Evidence\dns.txt"
# 同一测试运行中由 fixture/抓包/内核日志核对唯一 RequestId、五元组和 tun-in。
```

**通过：**TCP 收到 fixture 对应 RequestId 的响应；UDP 收到同 ID echo（不是仅 send 成功）；DNS 返回该唯一域名预期地址；三者在内核连接/路由日志均有 `inbound=tun-in`，并有对应 outbound 与 socket 五元组。公网 server 日志或受控抓包能佐证。任一 HTTP 204、代理 IP 查询、`Test-NetConnection` 单独成功均**不算**此项通过。脚本还应在**关闭 system proxy 且 mixed inbound 不监听**的窗口重复一遍，以排除误走 2085。

LAN/NAS/WSL/Tailscale 分别单独跑，不以 ping 代替：

```powershell
Test-NetConnection $LanIp -Port '<LAN TCP fixture port>' -InformationLevel Detailed
Test-NetConnection $NasIp -Port 445 -InformationLevel Detailed
Test-NetConnection $WslIp -Port '<WSL TCP fixture port>' -InformationLevel Detailed
Test-NetConnection $TailscaleIp -Port '<授权 tailnet fixture port>' -InformationLevel Detailed
# 独立 Windows Tailscale 才执行：
tailscale status; tailscale netcheck; tailscale ping $TailscaleIp
Get-NetRoute -DestinationPrefix '100.64.0.0/10' -ErrorAction SilentlyContinue
Get-NetRoute -DestinationPrefix $LanPrefix,$WslPrefix -ErrorAction SilentlyContinue
```

**通过：**每项 TCP fixture 实际应答且 `SourceAddress/InterfaceAlias` 是预期 LAN/WSL/Tailscale 接口，不是 SFW；SFW 日志无这些直连 fixture 的 `tun-in`；NAS 可额外执行一次授权测试共享的列目录，WSL 从 guest 访问一个 Windows 测试端口也成功。Tailscale 的 `tailscale ping` 与 TCP 均成功，`100.64.0.0/10` 及启动前更具体路由仍在。独立与内嵌 Tailscale 两组分别执行；若 profile 是内嵌 `ts-ep`，应看到 tailnet flow 进入 SFW 后交给 endpoint，并验证对端 TCP，不要求出现“无 tun-in”。

## 4. 分阶段实施与逐项门禁

所有阶段都从隔离测试 profile/临时端口开始；不安装到真实 Desktop 路径，不替换用户正在使用的服务。上一阶段失败即停止，保留现场，不带病进入下一阶段。

### P0：先封住认证和权限边界，不启用 TUN

实现：0.1–0.3；服务只提供 ping/version/recover 的 dry-run，不创建 adapter；Native/Go 二次实例和全部 Engine API 使用 token。更新 `scripts/test-windows-smoke.ps1` 读取 ACL 保护 token（`31-53` 当前裸请求会失效）。保留现有 system-proxy 单独行为。

```powershell
$env:GOTOOLCHAIN='go1.26.10'; $env:CGO_ENABLED='0'
$env:GOFLAGS='-mod=mod -tags=with_gvisor,with_quic,with_utls,with_clash_api'
make check; if ($LASTEXITCODE) { exit $LASTEXITCODE }
make build; if ($LASTEXITCODE) { exit $LASTEXITCODE }
.\scripts\build-native.ps1
# 在普通用户 PowerShell；读取 token 的命令不得写入报告。
$r = Get-Content "$env:LOCALAPPDATA\SFW\run\<instance-id>\runtime.json" -Raw | ConvertFrom-Json
$t = Get-Content "$env:LOCALAPPDATA\SFW\run\<instance-id>\engine.token" -Raw
$h = @{Authorization="Bearer $t"}
Invoke-WebRequest ($r.url+'/api/state') -SkipHttpErrorCheck | Select-Object StatusCode
Invoke-WebRequest ($r.url+'/api/state') -Headers @{Authorization='Bearer definitely-wrong'} -SkipHttpErrorCheck | Select-Object StatusCode
(Invoke-RestMethod ($r.url+'/api/state') -Headers $h).running
icacls "$env:LOCALAPPDATA\SFW\run\<instance-id>"
icacls "$env:LOCALAPPDATA\SFW\run\<instance-id>\engine.token"
icacls 'C:\Program Files\SFW\Core'
Get-Acl 'HKLM:\SYSTEM\CurrentControlSet\Services\SFWTun' | Format-List
```

另用第二个 Windows 测试用户读取 token、调用 state/activate 和 broker stop；预期 OS 拒绝读取或 401/403。再启动第二实例并只激活窗口，确认 Authorization 有效、不会自动导入深链、不会开第二个 engine。检查日志/报告没有 token。

**通过：**两个无效请求均 401、有效 200；所有写 API 也有同结果；token 目录仅预期 SID/SYSTEM，Program Files 用户不可写；当前用户 HKCU 正常，SYSTEM hive 未被写成用户代理；真实 token 检查如 0.1。**不通过则不得把 API 搬入服务。**

### P1：最小 TUN 创建/关闭，不接管流量

实现：受保护 host、单一 session、`auto_route=false,strict_route=false` 的诊断 TUN；接真实 sing-box profile，状态从 host 实况而非硬编码返回。只验证 adapter 和地址，不启用全局 DNS/route。

```powershell
# 由安装命令在测试机安装批准包；不得在桌面 exe 上注册服务/任务。
.\scripts\install-tun-service.ps1 -PackageRoot '<已签名测试包>' -RequireAdministrator
$r = Get-Content "$env:LOCALAPPDATA\SFW\run\<instance-id>\runtime.json" -Raw | ConvertFrom-Json
$h = @{Authorization="Bearer $(Get-Content "$env:LOCALAPPDATA\SFW\run\<instance-id>\engine.token" -Raw)"}
Invoke-RestMethod ($r.url+'/api/core/start') -Method Post -Headers $h
Invoke-RestMethod ($r.url+'/api/state') -Headers $h | ConvertTo-Json -Depth 8
Get-NetAdapter -IncludeHidden | Where-Object InterfaceDescription -Match 'Wintun'
Get-NetIPAddress -InterfaceAlias SFW
Invoke-RestMethod ($r.url+'/api/core/stop') -Method Post -Headers $h
```

**通过：**真实 `running/tun`、adapter identity、generation 一致；无默认路由变化；stop 后 adapter/session 和状态消失。记录失败时的 UAC/service/Wintun 错误，不能通过放宽全目录 ACL 来消除。

### P2：先单条 TCP，再默认路由/DNS

实现：`auto_route=true,strict_route=true`、精确 LAN/NAS/WSL 排除、DNS hijack、默认出口绑定；先只开放公网 TCP fixture，再启用 UDP/DNS。默认 system proxy 关闭，避免掩盖问题。

执行第 1、3 节依赖检查和 TCP 测试，保存 before/after；测试期间执行 `Get-NetRoute -AddressFamily IPv4,IPv6`、`Get-DnsClientServerAddress`、`route print`，确认 SFW 默认路由存在、LAN 排除与物理默认出口存在、DNS 指向设计值。

**通过：**TCP 真 socket + `tun-in` + fixture 回包三者一致；物理出口连接没有递归回 SFW；LAN 保持直连。若 TCP 失败，不继续以 HTTP 工具代替，不启用 UDP 验收。

### P3：UDP、DNS 和全流量接管

实现/校准 system stack UDP NAT、DNS 上游与规则、IPv6 策略。独立执行第 3 节 TCP、UDP、DNS，每项清理缓存/使用唯一 RequestId；保存 SFW 日志、server 日志/授权抓包。

**通过：**三项各自满足第 3 节证据要求；TUN 开启且 system proxy/ mixed listener 关闭仍成立；没有未经声明的 IPv6 漏流。然后独立开启 system proxy，确认 HKCU 值与偏好保留，TUN 四项仍通过。完成前 UI 文案只能是“实验中/未验证”，不能宣称完全对齐 SFM。

### P4：LAN/NAS/WSL 和 Tailscale 共存

实现动态发现、用户选择、排除变更审计；分别执行第 3 节命令。先做独立 Windows Tailscale，再做内嵌 netstack；`system_interface=true` 若未实现隔离/冲突检测，应明确阻止该 profile。

```powershell
Get-NetAdapter -IncludeHidden | Where-Object {$_.InterfaceDescription -match 'Wintun|Tailscale|Hyper-V'}
Get-NetRoute | Where-Object {$_.DestinationPrefix -in @($LanPrefix,$WslPrefix,'100.64.0.0/10')} |
  Sort-Object DestinationPrefix,InterfaceIndex | Format-Table
Restart-Service 'Tailscale'   # 仅独立 Tailscale 隔离测试机；不得在用户生产服务上执行
# 等待 Tailscale 上线后重跑 tailscale ping 和 TCP fixture。
wsl --shutdown               # 仅隔离测试 WSL；不得用于真实工作 WSL
wsl hostname -I              # 更新 WslIp/WslPrefix，再验证动态排除和双向 TCP
```

**通过：**LAN/NAS/WSL 新网段均保持直连，Tailscale peer/MagicDNS 按所属模式工作，SFW 不删除对方 route/DNS；冲突 profile 有明确错误而非网络全断。生产机上跳过 Restart-Service/WSL shutdown，不把跳过写成通过。

### P5：正常停止、强杀、服务崩溃恢复

实现完整 journal、进程 identity、broker health/reconcile 和用户代理恢复。每种故障用独立全新 baseline；记录 before/running/after 三组第 3 节快照。先正常 stop，再运行强杀：

```powershell
$hostPid = (Get-Process sing-box-win-tun).Id
Stop-Process -Id $hostPid -Force
Wait-Process -Id $hostPid -ErrorAction SilentlyContinue
# 不启动新 TUN；调用窄恢复命令，等待 broker 完成并取回对账结果。
.\scripts\recover-tun-windows.ps1 -ExpectedGeneration '<running 时 generation>' -ResultPath "$Evidence\recovery.json"
Get-Content "$Evidence\recovery.json"
Get-Process -Id $hostPid -ErrorAction SilentlyContinue
Get-NetAdapter -IncludeHidden | Where-Object InterfaceDescription -Match 'Wintun'
Get-DnsClientServerAddress; Get-DnsClientGlobalSetting
Get-NetIPInterface | Select-Object InterfaceAlias,AddressFamily,WeakHostSend,Forwarding
route print
```

随后分别测试 broker 强杀后启动恢复、agent 强杀后恢复 HKCU、服务+agent 同时异常、休眠/唤醒、换默认网卡。不得在这些测试中重启整机；重启/注销留到专门隔离验收。

**通过：**正常 stop 和**每种强杀**都恢复路由、DNS、默认出口、被 SFW 改过的 weak-host、原用户代理；不存在 SFW adapter/WFP/session；用户后来手改的 DNS/proxy 不被覆盖；第二次 recover 幂等且不碰其他应用。恢复失败则该场景不通过，UI 明示残余。不得以“下次启动一般能恢复”替代强杀后立即对账。

### P6：发布前完整回归

```powershell
make check; if ($LASTEXITCODE) { exit $LASTEXITCODE }
make build; if ($LASTEXITCODE) { exit $LASTEXITCODE }
.\scripts\build-native.ps1
powershell -NoProfile -File .\scripts\test-windows-smoke.ps1 -ResultPath "$env:TEMP\sfw-smoke.json"
powershell -NoProfile -File .\scripts\test-windows-service-security.ps1 -ResultPath "$env:TEMP\sfw-security.json"
# 对 P2-P5 每个场景重新执行；JSON 标记 skip 不得汇总成 pass。
```

**通过：**Go/VS 实际日志成功，普通代理回归、TUN 四项、共存、正常/异常恢复、权限和二次激活都有独立证据；保存构建 hash 和测试 commit。升级/卸载后再次对账，不能残留服务路径或 SFW 网络状态。本文档阶段命令中的未来脚本属于对应实现交付物，不能拿本稿单独运行来宣称验收完成。

## 5. 仍需确认/未验证事项

1. 专用服务账户能否覆盖当前 wintun/NETIO/WFP 全部操作需 P0/P1 token 实测；未实测前保留最小 LocalSystem host，不虚构权限结论。
2. `strict_route` 与具体 Tailscale 驱动版本、第三方 VPN/EDR 的 WFP 共存必须实机验证；不提供“关闭所有过滤器”的默认绕过。
3. 多用户首版建议单 TUN owner；是否要求多会话同时 TUN、代理出口按会话分流仍是产品问题。
4. WSL2 mirrored networking、Hyper-V NAT 变化和 IPv6 LAN 行为需按测试机版本补矩阵；写死 NAT prefix 不可接受。
5. 内嵌 `ts-ep system_interface=true` 会形成第二 TUN，首版应检测并阻止/明确实验状态，除非另做双 TUN 生命周期设计与验收。
6. SFM 的 `excludeLocalNetworks/includeAllNetworks/enforceRoutes` 与 Windows route exclusion 不是一一等价；本稿对齐实际接管、排除、恢复语义，不宣称 GUI/系统扩展逐项等价。
7. 当前容器没有 Go 工具链、Windows/VS，未构建、未启动服务、未修改任何配置，未进行重启、休眠、换网、UDP/DNS/Tailscale 实测。Paperclip 审核通过和上述分阶段实机证据齐全后，才能把 TUN 标为完成。
