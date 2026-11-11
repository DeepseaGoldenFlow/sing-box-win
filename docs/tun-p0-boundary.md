# P0：认证与权限边界（不启用 TUN）

对应 `docs/proposal-tun-windows.md` §0.1–§0.3 与 §4「P0」。**本阶段没有创建 TUN
adapter，也没有修改全局 DNS/路由；系统代理仍是原有的 HKCU/WinINET 单用户行为，
与 TUN 无关。** 通过本页的检查只说明认证与权限边界成立，不表示 TUN 可用。

## 1. 进程与信任边界

| 进程 | 身份 | 本阶段职责 |
|---|---|---|
| `SingBoxWin.Native.exe` | 交互用户，普通完整性，不要求管理员 | 展示、编辑、请求启停；不接触 token 以外的权限 |
| `sing-box-win.exe`（每用户 engine） | 同一交互用户 SID/会话 | 订阅/profile/设置、loopback API、**自己的 HKCU** 系统代理 |
| `sfw-tun-broker.exe`（Windows 服务，可选） | 服务身份（当前为 LocalSystem） | 只回答 `hello`/`ping`/`version` 和 **dry-run** `recover`；不建 adapter、不碰路由/DNS、不接收 profile/订阅/任意路径 |

P0 的 broker 没有 TUN host，install 脚本只是交付物，由 JARVIS 在批准的测试机上
执行；不得从 Desktop、Downloads、repo 或 `build/` 注册服务。

## 2. Engine API 的 bearer token（§0.2.1–§0.2.4）

- 每次 engine 启动用 OS CSPRNG 生成 32 字节 token（base64url，无 padding），
  不派生自 PID、用户名、时间戳或 Clash secret；重启即轮换。
- 明文只写 `%LOCALAPPDATA%\SFW\run\<instance-id>\engine.token`。目录和文件使用
  **显式、非继承** DACL：仅当前用户 SID 与 `SYSTEM`；不含 `Everyone`、`Users`、
  `Administrators` 的继承授权。文件经受保护的临时文件 + 原子 rename 写入。
- `runtime.json` 只含公开元数据：
  `url,pid,process_start_time,instance_id,user_sid,session_id,protocol_version`，
  永不含 token。读取方校验协议版本、loopback URL、PID 启动时间、instance id，
  并拒绝链接/重解析点与属主不符的 token 文件。
- **所有** `/api/*`（含 `/api/state`、core stop、quit、`/api/activate`）先过
  `Authorization: Bearer <token>` 再进路由；缺失、空、错误 scheme、错误 token
  一律 401，比较使用常量时间。Host/Origin 检查仍保留，但只是纵深防御。
- `/api/activate` 额外要求 `X-SFW-Instance` 与当前 instance 一致；空 payload
  只表示“已有实例接管”，不导入任何链接或文件。

## 3. 二次实例与 UI 激活（§0.1、§0.2.5）

- engine 单实例 mutex 名为 `Local\sing-box-win-<SID>`，带显式 DACL（仅该 SID 与
  SYSTEM）；WinUI 的激活 mutex/event 同样按 SID 命名并带 DACL
  （`Local\SingBoxWinNative-<SID>`、`Local\SingBoxWinNativeActivate-<SID>`）。
- 第二个实例只能发出 `SetEvent` 让主窗口前置；不会启动第二个 engine，也不会
  因为“再次启动”而自动导入深链。深链/文件关联导入仍必须经过 engine 的
  authenticated `/api/activate`（token + instance），由已运行的 UI 记录结果。
- 其他 SID/会话无法打开或触发这些对象，也无法读取/使用 token。
- §0.2.5 要求的“校验每用户 SID/session”由运行目录 ACL 承担：`runtime.json`
  在带显式 DACL 的目录内，`engine.token` 只有该用户 SID 与 `SYSTEM` 可读，因此
  能在 `/api/activate` 出示正确 token 的进程必然是该用户（或管理员/SYSTEM，
  §0.2.2 已说明不宣称抵御本机管理员）；跨 SID 的调用在读取 token 时就被 OS
  拒绝，即使拿到 URL 也只能得到 401。engine 另用 `X-SFW-Instance` 把请求绑定到
  产生该 runtime.json 的具体进程。

## 4. HKCU 系统代理的唯一属主（§0.3）

- 代理读写只发生在交互用户的 engine 进程，写的是它自己的 `HKEY_CURRENT_USER`
  并调用 `InternetSetOptionW` 通知 WinINET。
- `ReadProxy`/`WriteProxy`/`AutoStart` 在 Windows 上先拒绝 LocalSystem：服务身份
  既不能写 SYSTEM 的 HKCU，也不能加载用户 hive 冒充用户设置。
- 备份含原 `ProxyEnable/ProxyServer`、SID、instance 与 SFW 写入值；只有当前值仍
  等于 SFW 写入值时才恢复，用户后来改动不覆盖。

## 5. Broker（§0.1、§0.2.6）——P0 仅 dry-run

- 服务端每次启动生成独立的服务 token/HMAC key 与 generation；capability 绑定
  user SID、session、instance、generation 和 2 小时到期时间，重启后旧 capability
  失效。
- pipe 名 `\\.\pipe\SFW\TUNBroker`，SDDL 允许 `SYSTEM` 与交互用户连接，但每个
  请求还要 `ImpersonateNamedPipeClient` 取真实调用者身份；请求体里的身份声明
  不被信任。
- 动作白名单只有 `hello`、`ping`、`version`、`recover`；`recover` 强制
  `dry_run=true`，只返回 `dry-run: ...` 计划行，不做任何系统修改。协议结构里没有
  profile/URL/路径/命令行字段。
- 人工检查（服务已安装时）：`sfw-tun-broker.exe hello|ping|recover` 以命名 pipe
  客户端身份走完 hello→capability→ping/recover。`hello` 的 `broker` 字段是服务
  自己的 token 事实（SYSTEM、session 0），用于证明 UI 与 broker 身份不同；普通
  用户无需也不应能打开 SYSTEM 进程的 token。

## 6. JARVIS 需要执行的验证

在批准测试机的普通用户 PowerShell（engine 已启动，原生客户端已构建）：

```powershell
make check; if ($LASTEXITCODE) { exit $LASTEXITCODE }
make build; if ($LASTEXITCODE) { exit $LASTEXITCODE }
.\scripts\build-native.ps1

powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-windows-smoke.ps1 `
  -ResultPath "$env:TEMP\sfw-smoke.json"
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-windows-service-security.ps1 `
  -ResultPath "$env:TEMP\sfw-security.json"
```

预期：

- `test-windows-smoke.ps1` 输出 `ok=true`；其中无 token 的 `/api/state`、错误
  bearer 的 `/api/state`、无 token 的 `/api/core/stop` 均断言为 401。
- `test-windows-service-security.ps1` 输出 `pass=true`，逐项为 PASS：
  token 目录/文件 ACL 仅用户 SID+SYSTEM、无继承、非重解析点；`runtime.json`
  不含 token；`C:\Program Files\SFW\Core` 不被 users 写；HKCU 与
  `state.system_proxy` 一致且 SYSTEM hive 未被写成用户代理；无 token / 错误
  token / 缺 instance 头的 activate 均 401，空 activate 返回
  `activated=false` 且未新增 profile。
- 未安装 broker 时，broker token 行会明确 FAIL/`not-installed`（预期，直到安装
  批准包）；安装后带 `-BrokerPath 'C:\Program Files\SFW\Core\sfw-tun-broker.exe'`
  重跑，应显示 UI 为交互用户、broker 为服务身份且会话不同，并且 `hello`/`ping`
  成功、`recover` 只返回 `dry-run:` 计划。

第二个 Windows 用户（脚本结果里的 `manual` 清单）：

- 读取 `...\engine.token`：OS 拒绝；
- `/api/state`、`/api/activate`：401；
- `sc.exe stop SFWTunBroker`：拒绝访问（5）；
- 同一用户启动两次客户端：只有窗口前置，没有第二个 engine，也没有自动导入。

最后检查 `engine.log`、`native-error.log` 与两个 result JSON 中检索 token 值，
预期 0 处命中。

## 7. 本阶段明确未完成

- 没有 TUN adapter、路由、DNS、WFP 改动；P1 起才实现。
- 未在真实 Windows 上验证 NTFS DACL、服务身份、pipe 模拟与实际 UI/broker
  token；本仓库的容器只能做 Go 交叉编译和单元测试。
- 原生 C# 客户端未在本环境编译。
- broker 尚未安装到任何机器；安装脚本要求签名包并由管理员在测试机执行。
- “低于 LocalSystem 的服务账户是否够用”按 proposal §5.1 留待 P1 token 实测。
