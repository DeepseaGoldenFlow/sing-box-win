# SFM 功能规格（sing-box for Apple）— Windows 客户端开发规格

> 注意：下文勾选代表 SFM 的能力清单，不代表 SFW 已完成。当前实现与验收缺口以 [SFW 整体审查](sfw-sfm-parity-audit.md) 为准。

> 结论先行：SFM 的**主框架是「左侧导航 + Dashboard 卡片网格」**，导航只有 6 项：**Dashboard / Groups / Connections / Logs / Tools / Settings**（Groups、Connections 仅在服务运行且存在分组/连接时才出现）。**没有独立的 Profiles 页面**——配置（Profile）通过 Dashboard 上的 Profile 卡片 + Profile 选择弹窗管理。SFM 是 Apple Network Extension/系统代理专用实现，很多「端口 / DNS / 允许局域网」并不是 App 级开关，而是**写进配置文件的字段**；Windows 端需要把这些从「配置文件层」提升为 UI（下文逐条标注）。
>
> 所有 [已证实] 项均来自对 SagerNet/sing-box-for-apple（dev 分支）源码与 Info.plist 的直接阅读；[推断] 项为基于源码结构的合理外推或 Apple 平台特有、Windows 需另行设计的部分。

---

## 1. 页面 / 导航结构

### 1.0 导航总览 [已证实]
`NavigationPage` 枚举（`ApplicationLibrary/Views/NavigationPage.swift`）定义的全部页面：

| 枚举 | 标题 | SF Symbol 图标 | 何时可见 |
|---|---|---|---|
| `dashboard` | Dashboard | `text.and.command.macwindow` | 总是 |
| `groups` | Groups | `rectangle.3.group.fill` | 已连接且 `commandClient.groups` 非空 |
| `connections` | Connections | `list.bullet.rectangle.portrait.fill` | 已连接 |
| `logs` | Logs | `list.bullet.rectangle` | 总是 |
| `tools` | Tools | `terminal.fill` | 总是（带红点 badge 计数） |
| `settings` | Settings | `gear.circle.fill` | 总是 |

- macOS 用 `NavigationSplitView`：**左侧 Sidebar（宽 150pt，`listStyle(.sidebar)`，不可滚动）+ 右侧详情（宽 650pt，NavigationStack）** [已证实]（`MacLibrary/MainView.swift`）。
- 未连接时 Sidebar 显示全部页面；连接后 Dashboard 置顶，Groups/Connections 插入 [已证实]（`SidebarView.swift`）。
- **工具栏**：右上常见 Start/Stop 按钮、Dashboard 页出现「Dashboard Items」按钮；iOS 在 topBarTrailing 放「Others」菜单（Dashboard Items / 远程服务器）[已证实]。
- SFI(iOS) 底部 Tab：Dashboard / Logs / Tools / Settings（iOS 无侧栏）[已证实]。

### 1.1 Dashboard（核心页）[已证实]
`DashboardView → ActiveDashboardView → OverviewView`。布局为 **纵向卡片网格**（`ScrollView` + `VStack(spacing:16)`），部分卡片两两成对同行（`Spacing:16`，各占一半宽）：
- **可配置卡片**（`DashboardCard` 枚举，用户可增删/排序，见 `CardManagementSheet`）：
  - `uploadTraffic`（Upload）、`downloadTraffic`（Download）——成对同一行（pairGroup=.traffic）
  - `status`（Status）、`connections`（Connections）——成对同一行（pairGroup=.statistics）
  - `httpProxy`（System HTTP Proxy）——单独一行
  - `clashMode`（Clash Mode）——单独一行（`clashModeList` 多于 1 项才显示）
  - `profile`（Profile）——**不可关闭**，始终存在
- **默认顺序**：`[upload, download, status, connections, httpProxy, clashMode, profile]` [已证实]。
- 未连接时仅显示 Profile 卡片，其余隐藏；Profile 卡片可切换/编辑/新建配置 [已证实]（`OverviewView.shouldShowCard`）。
- 卡片容器 `cardStyle()`：**圆角 16**，macOS 背景 `textBackgroundColor`（iOS 二级分组背景；iOS26+ 用 glassEffect）[已证实]（`ViewModifiers.swift` `CardStyleModifier`）。

各卡片内容（逐条，见 §2 Dashboard）：
- Status 卡：Memory（内存）、Goroutines（协程数）
- Connections 卡：Inbound / Outbound 计数字段
- Upload / Download 卡：大字号「当前速率 xx/s」+ 小字「累计总量」+ **折线流量图**（30 点环形缓冲，高 60pt，实线 2pt + 10% 填充 + 虚线网格 3 格）
- System HTTP Proxy 卡：标题 + 右侧开关（`Toggle`，macOS `.switch` 样式）
- Clash Mode 卡：Mode 标题 + 模式切换（tab 胶囊 / 菜单二选一，模式来自 clash `modeList`）
- Profile 卡：当前配置名 + 切换按钮 + 编辑/更新/分享（QR/QRS/文件）按钮行

**Dashboard Items 管理**（`CardManagementSheet`）：列表含拖拽手柄 + 每卡开关（Profile 卡禁用不可关），右上「Reset」恢复默认，桌面端 Done/Cancel [已证实]。

### 1.2 Groups（策略组页）[已证实]
`GroupListView → GroupHeaderView + GroupContentView`：
- 每个分组一个「卡片段」（上圆角 header + 下圆角内容，`cardSegment`）：
  - Header：组名(tag) + 类型(displayType) + 节点数(胶囊 badge) + 「URLTest 一键测速」按钮(bolt) + 展开/收起(chevron)
  - 内容：展开态为自适应网格(itemMinWidth 170)，每节点卡显示：**节点名 + 协议类型(caption) + 延迟(delayString，按延迟着色)**；选中项描边高亮 + 12% accent 填充
  - 收起态为「点阵」预览（每个节点一个小方块，按延迟上色，选中项中心白点）
- 交互：点击节点＝切换出站(selector/urltest 才可选)；右键节点＝URLTest；点组 header＝展开/收起 [已证实]。

### 1.3 Connections（连接页）[已证实]
`ConnectionListView`：
- 列表（`LazyVStack`，每行 `ConnectionView`），空状态分文案（No connections / No active connections / No closed connections / No matching connections）
- 工具栏菜单：**State 筛选（All / Active / Closed）+ Sort By（Date / Traffic / Traffic Total）+ Close All Connections（destructive）**；macOS 有搜索框（`searchable`）
- 单条点击进入 `ConnectionDetailsView`（可单条关闭）[已证实 列表/筛选/全部关闭；详情页单条关闭 推断自 ConnectionDetailsView 文件名]

### 1.4 Logs（日志页）[已证实]
`LogView → LogViewContent`：
- 文本视图（等宽/ANSI 着色 `ANSIColors`），可暂停(pause)、搜索(magnifyingglass → searchable)
- 右上菜单：Log Level 过滤（Default / Error / Warn / Info / Debug / Trace）、Save（导出 logs.txt）、**Copy to clipboard**、Clear Logs
- 可选远程服务器日志 [已证实]。

### 1.5 Tools（工具页）[已证实，Apple 专属多在 Windows 可裁剪]
`ToolsView`，按 Section 分组：
- **Endpoints**：Tailscale（每 endpoint 一条）、OpenConnect、OpenVPN（每个 endpoint 一条），另可「Connect via SSH」
- **Services**：USB/IP（每 server 一条）
- **Network**：STUN 测试、Network Quality（网络质量）
- **其它**：Crash / OOM / Power 报告列表、Taildrop、Ghostty 终端配置
> Windows 建议：Tools 只需保留与 sing-box 内核相关的**端点(endpoint)状态**（尤其 Tailscale endpoint），其余 Apple 系统扩展类工具可省略。

### 1.6 Settings（设置页）[已证实]
`SettingView` 分节（`SettingsPage` 枚举）：
- **App**（AppView）：Language 语言选择、Cache Size + Clear Cache、（macOS）Start At Login、Show in Menu Bar、Real-time Speed(菜单栏速度：Disabled/Enabled/Unified)、Keep Menu Bar in Background、Update Track(Stable/Beta)、GitHub Token、Automatic Update Check / Check Update、System Extension 安装/更新/卸载、Helper Service 安装/更新/卸载
- **Core**（CoreView）：Version（内核版本 `LibboxVersion()`）、Data Size（工作目录占用）、Open/Destroy 工作目录、Disable Deprecated Warnings(beta)
- **Packet Tunnel**（PacketTunnelView）：includeAllNetworks / excludeAPNs / excludeCellularServices / excludeLocalNetworks / enforceRoutes / excludeDeviceCommunication + Reset
- **On Demand Rules**（按需连接规则，Apple 特有）
- **Profile Override**（ProfileOverrideView）：Hide VPN Icon(excludeDefaultRoute) / No Default Route(autoRouteUseSubRangesByDefault) / Exclude APNs Route + Reset
- **Remote Control**（远程控制其它设备）
- **Sponsors** / **About**（文档、源码、评分链接）
> ⚠️ 注意：Settings 里**没有**「端口 / DNS / 允许局域网」这类网络参数开关——它们属于配置文件字段（见 §3）。

---

## 2. 功能清单（逐条可勾选）

### A. 配置管理（Profile）
- [x] 导入：**从文件**（.json 或 .bpf 二进制配置）[已证实]（`NewProfileMenuView.handleFileImport`）
- [x] 导入：**扫描二维码**（支持远程订阅链接 QR，或 QRS 二进制分片 QR）[已证实]（`QRScannerView` + `LibboxParseRemoteProfileImportLink`）
- [x] 导入：**从 iPhone/iPad 导入**（仅 tvOS，DeviceDiscovery）[已证实]
- [x] 新建：**手动创建**（Create Manually，填 Name / Type / URL / 文件）[已证实]
- [x] 导入方式：**粘贴/剪贴板** [推断——源码未见独立"粘贴"入口；Windows 建议提供粘贴 JSON/链接]
- [x] 导入方式：**URL 订阅**（Type=Remote + URL）[已证实]
- [x] 导入方式：**深层链接** `sing-box://` + 文档类型 `.bpf` [已证实]（Info.plist `CFBundleURLSchemes=sing-box`，`MainView.onOpenURL → viewModel.openURL`）
- [x] 配置类型：**Local / iCloud / Remote** [已证实]（`ProfileType`：local=0, icloud, remote）
- [x] 配置编辑：改 Name / URL / Auto Update / Interval（类型不可改）[已证实]（`EditProfileView`）
- [x] 启用/禁用：**通过「选中」当前 Profile 生效**（无 per-profile 开关）[已证实]（`selected_profile_id`）
- [x] 分组：**无分组概念**；靠 Profile 列表排列/多配置切换 [已证实]
- [x] 重命名：编辑 Name 即重命名（`uniqueName` 去重）[已证实]
- [x] 删除：列表 `onDelete` + 确认 [已证实]（`ProfilePickerSheet.deleteProfile` / `ProfileManager.delete`）
- [x] 排序：列表 `onMove` 拖拽排序 [已证实]
- [x] 复制：**无原生"复制"**；可「Save Content JSON」导出后重新导入实现 [已证实导出 / 推断复制]
- [x] 分享/导出：Share URL as QR Code / Share as QRS Code / Save File / Share File / Save Content JSON / Share Content JSON File [已证实]（`ProfileCard`、`ProfilePickerSheet`）
- [x] Remote 订阅：Auto Update 开关 + Auto Update Interval（**单位：分钟**，默认 60）[已证实]
- [x] 配置切换：Dashboard 的 Profile 选择弹窗（`ProfilePickerSheet`，带勾选标记、编辑、删除、排序、新建）[已证实]

### B. 内核控制
- [x] 启动 / 停止：工具栏 Start/Stop 按钮（`StartStopButton`，图标 play.fill/stop.fill）[已证实]
- [x] 重启：`ExtensionProfile.restart()`（停→等待 disconnected→启）[已证实]
- [x] 状态显示：`NEVPNStatus`(connecting/connected/disconnecting/disconnected) + 运行时长 `RuntimeDurationText` + 连接状态卡 [已证实]
- [x] 错误展示：Alert（启动失败/重启超时/`fetchLastDisconnectError`）+ 命令客户端 `lastError` [已证实]
- [x] 空配置禁用启动按钮（`environments.emptyProfiles`）[已证实]

### C. 运行模式
- [x] **TUN**：SFM 全程以 NE PacketTunnel（TUN）方式运行；macOS 需 System Extension + Helper Service（**管理员/权限**流程：Install → 系统设置批准）[已证实]
  - Windows 映射：TUN 需管理员权限（wintun/tun 驱动）；建议首次启动提权 [推断]
- [x] **系统代理（HTTP/SOCKS）**：`systemProxyEnabled` 开关（默认 true）+ Dashboard「System HTTP Proxy」卡开关 [已证实]（`HTTPProxyCard`）
  - 端口/协议由配置决定；Windows 映射：改 WinINET 系统代理指向 HTTP/SOCKS inbound [推断]
- [x] **全局 / 规则 / 直连 模式**：Clash Mode 卡切换（rule/global/direct，来自内核 clash `modeList`）[已证实]
- [x] **仅代理应用 / 分流**：由配置 `route.rules` 控制（App 不自带"仅代理某应用"UI）[推断]
- [x] TUN 扩展参数见 Settings→Packet Tunnel / Profile Override（& §2 H）[已证实]

### D. Dashboard（实时数据）
- [x] ↑/↓ 流量**实时折线图**（30 点缓冲，1s 刷新）[已证实]（`TrafficLineChart`，`CommandClient.writeStatus` `statusInterval=1s`）
- [x] 当前速率 + **累计总流量**（uplink/downlink、uplinkTotal/downlinkTotal）[已证实]（`UploadTrafficCard`/`DownloadTrafficCard`）
- [x] **连接数**：Inbound / Outbound [已证实]（`ConnectionsCard`）
- [x] **活动内存**(memory) + **协程数**(goroutines) [已证实]（`StatusCard`）
  - 数据来源：SFM 用内核**进程内** CommandClient（`LibboxStatusMessage`），Windows 用 **Clash API HTTP**：`GET /traffic`（↑↓）、`GET /memory`（inuse/goroutines 或 `/connections` 计数）、`GET /connections` [推断映射，字段对应内核])

### E. Connections（连接）
- [x] 活动连接列表（inbound/outbound/规则/目标/流量/时间等详情）[已证实]
- [x] 搜索（关键字）[已证实]
- [x] 筛选：State（All/Active/Closed）+ Sort（Date/Traffic/Traffic Total）[已证实]
- [x] **单条关闭**（详情页）[推断自 `ConnectionDetailsView`；内核 API 支持 `DELETE /connections/{id}`]
- [x] **全部关闭**（Close All Connections）[已证实]（`closeAllConnections`）

### F. 节点 / 策略组
- [x] 延迟测试：**单条**(右键节点 URLTest) / **整组**(header bolt 按钮)`performGroupURLTest` [已证实]
- [x] 当前选中节点高亮 [已证实]
- [x] 自动选择：`urltest` 类型组由内核自动择优选延迟最低节点；`selector` 手动 [已证实]
- [x] 节点信息：名称 / 协议(displayType) / 延迟(ms，按延迟着色) [已证实]（`OutboundGroupItem` `urlTestDelay`→`delayString "xxms"`）

### G. 日志
- [x] 级别筛选：Default / Error / Warn / Info / Debug / Trace [已证实]（`LogLevel`）
- [x] 关键字过滤（搜索框实时过滤）[已证实]
- [x] 暂停 / 清空 [已证实]
- [x] 导出：Save（写 `logs.txt`）/ Copy to clipboard [已证实]（`LogExportView`）
- [x] 日志环形缓冲上限（`max_log_lines` 默认 300，seeded batch 3000）[已证实]

### H. 设置
- [x] **开机自启**（Start At Login，`SMAppService`）[已证实]
- [x] **静默启动 / 后台驻留**（Show in Menu Bar + Keep Menu Bar in Background）[已证实]
- [x] **菜单栏图标**（Show in Menu Bar 开关）+ **实时速率显示模式**（Disabled / Enabled / Unified）[已证实]
- [x] **语言**（Language 选择，重启生效）[已证实]
- [x] **主题**：App 无独立主题切换，随系统深/浅色；仅终端 Ghostty 有独立主题 [已证实],[主题开关 推断/不适用]
- [x] **端口**：App 无 UI 端口设置；tvOS 有 `commandServerPort`；macOS 用进程内 client [已证实→端口非 App 级 推断]
  - Windows 映射：需在设置里暴露「Clash API / 混合代理 HTTP·SOCKS 端口」[推断]
- [x] **DNS**：无 App 级 DNS 设置，DNS 由配置文件 `dns` 段控制 [推断/不适用]
- [x] **允许局域网**：无直接开关；由 inbound `listen` + 系统防火墙决定 [推断/不适用]
- [x] **订阅自动更新间隔**：per-profile Auto Update Interval（分钟）[已证实]
- [x] **核心版本查看**（Core→Version）/ **更新**（GitHub Releases / App Store；Update Track Stable·Beta、GitHub Token、自动检查）[已证实]

### I. 其它（重点）
- [x] **Endpoint 支持（重点）**：配置文件 `endpoints` 数组**原样透传**给内核；Tools 对每个 endpoint（Tailscale / OpenConnect / OpenVPN）有独立状态页 [已证实 configContent 原样传入 + ToolsView Endpoints 分组]
  - **Tailscale endpoint 必须透传**：SFM 生成/传递的配置内容不做裁剪，`endpoints:[{type:"tailscale"...}]` 完整交给 sing-box 内核；Windows 端生成 config.json 时**禁止剥离 endpoints**，且要能显示其状态 [已证实]
  - Tailscale endpoint 详页：State(backendState)、本机设备、Exit Node 选择、Taildrop 收发、Auth URL / Auth URL QR、Peer 列表（在线点/badge/IP）+ SSH [已证实]（`TailscaleEndpointView`）
- [x] **剪贴板导入** [推断——Windows 建议补；SFM 靠系统分享/文件导入]
- [x] **深层链接导入**：`sing-box://` scheme + `.bpf` 文件关联 [已证实]（Info.plist）
- [x] 订阅更新后台任务（`backgroundTaskID = *.update_profiles`）[已证实]

---

## 3. 数据与接口

### 3.1 配置存储（SFM，Apple）[已证实]
- **元数据**：GRDB **SQLite** 库，表 `profiles`；字段：`id, name, order, type, path, remoteURL, autoUpdate, autoUpdateInterval, lastUpdated, userAgent`（`Library/Database/Profile.swift`）。
- **选中态**：`selected_profile_id`（SharedPreferences，默认 -1）。
- **配置内容**：每个 Profile 的 JSON 文本单独存文件，路径记在 `path`；工作目录：
  - macOS：`<AppGroup>/Library/Caches/Working/`
  - iOS：`<AppGroup>/Library/Caches/Working/`
  - iCloud 类型的配置文件位于 `iCloud Drive/sing-box/<name>`（`FilePath.swift` + `NewProfileView` footer）
- **其它偏好**：`SharedPreferences`（键值库，含 systemProxyEnabled、includeAllNetworks、excludeDefaultRoute、dashboard 卡片配置、日志行数、连接筛选/排序、语言等）[已证实]（`SharedPreferences.swift`）。

### 3.2 与内核交互（SFM，Apple）[已证实]
- **配置投递**：App 取选中 Profile 的 JSON 文本 → `options["configContent"] = <原文>`，连同 override 开关（`includeAllNetworks / systemProxyEnabled / excludeDefaultRoute / autoRouteUseSubRangesByDefault / excludeAPNsRoute / oomKiller* / powerReportEnabled / locale`）通过 `ExtensionProfile.start()` → NE `startVPNTunnel(options:)` 交给隧道扩展；扩展内 `commandServer.startOrReloadService(configContent, options:)` 交给 Go 内核（Libbox）[已证实]（`ExtensionProfile.prepareStartOptions`、`ExtensionProvider.startService`）。
- **override 由内核施加**：include_all_networks→TUN 栈转 gvisor；exclude_default_route→追加 `0.0.0.0/31`、`::/127` 到 route_exclude_address；excludeAPNsRoute→`push.apple.com` 进 bypass_domain、`17.0.0.0/8` 进 route_exclude_address；auto_route 分段路由 [已证实 注释/字段]。
- **状态/日志/分组/连接/模式**：全部通过**进程内 `LibboxCommandClient`**（非 HTTP）；`statusInterval=1s` 推送 traffic、memory、goroutines、connectionsIn/Out；另有 `LibboxCommandLog / Group / Connections / ClashMode / Outbounds` 订阅命令；日志 backlog 首条推送后增量、100ms 批量合并 [已证实]（`CommandClient.swift`）。
- **启动/停止/重启**：NE VPN 会话 start/stop；重启＝stop→轮询 disconnected(≤5s)→start [已证实]。

### 3.3 Windows 端接口建议 [推断，基于 SFM 数据流对齐]
1. **配置生成**：读取 Profile JSON 原文 → 写入工作目录 `config.json`；**必须原样保留 `inbounds / outbounds / route / dns / endpoints`**；可按 UI 开关向 `route.route_exclude_address` / `route.rules` 增补（复刻 Profile Override 语义）。
2. **注入 Clash API**：为对齐 SFM 的实时数据，向 `experimental.clash_api` 写 `external_controller: 127.0.0.1:<port>` (+secret)，然后 HTTP 拉：
   - `GET /traffic`（↑↓ 实时）→ 折线/速率/总量
   - `GET /memory`（inuse/goroutines）→ Status 卡
   - `GET /connections`（含 upload/download/rule/chains/start）→ Connections 列表 + Inbound/Outbound 计数
   - `GET /proxies`（groups + now + 历史延迟）→ Groups 页；`GET /proxies/{name}/delay`（测速）；`PUT /proxies/{group}`（选择节点）
   - `GET /configs`→ modeList；`PATCH /configs`（`{"mode":...}`）→ Clash Mode 卡
   - `DELETE /connections`（全部关闭）/`DELETE /connections/{id}`（单条）
   - 日志：`GET /logs?level=`（SSE/websocket 流）
3. **日志**：维护环形缓冲（默认 300/3000 行）+ 级别过滤 + 关键字 + 导出 txt。
4. **内核版本**：读 `sing-box version`；更新走 GitHub Releases（Stable/Beta track 对齐）。
5. **TUN/系统代理**：TUN 需管理员（提权启动 + wintun）；系统代理改 WinINET/注册表（HTTP/SOCKS 指向 inbound）。

---

## 4. UI 细节（照着画）

### 4.1 配色 / 主题
- **深色优先**：SFM 随系统外观，深色即背景近黑、卡片为略亮的分组背景 [已证实 用系统色]。
- macOS 卡片背景 `NSColor.textBackgroundColor`；iOS `secondarySystemGroupedBackground`；iOS26+/macOS26+ 用 `glassEffect(.regular, rect(cornerRadius:16))` [已证实]。
- 文本主色 `.primary`/`.label`；次要 `.secondary`；accent 用系统 accentColor；延迟着色：低延迟绿、高延迟红（`delayColor`/`urlTestNeutral`）[已证实]。

### 4.2 导航布局
- 桌面：**左侧 Sidebar（150pt，sidebarList 样式，不滚动）**，右侧详情 650pt；`NavigationSplitView` [已证实]。
- 侧栏行＝`Label(title, systemImage:)`，Tools 行带红点 badge（工具告警+失败会话数）[已证实]。
- iOS：底部 Tab（Dashboard/Logs/Tools/Settings），或在侧栏可用宽度足够时切换 `SidebarLayout` [已证实]。

### 4.3 卡片与圆角
- 卡片**圆角 16**、内边距 **16**（tvOS 26）、卡片内元素纵向间距 8–12 [已证实]。
- 节点/连接小块圆角 **12**（`RoundedRectangle(cornerRadius:12)`，选中 12% accent 填充 + 1pt 描边）[已证实]。
- 分段卡（Groups 组）：header 上圆角 16、body 下圆角 16，中间直角拼接 [已证实 `CardSegmentShape`]。
- 小图标按钮：44×32，圆角 8，`Color.secondary.opacity(0.1)` 底 [已证实 `ActionIconButton`]。

### 4.4 开关 / 控件
- 开关：系统 `Toggle`，macOS 用 `.switch` 样式（胶囊滑块）[已证实]。
- 卡片网格间距 16；Dashboard 每行两卡各占一半 [已证实]。
- 模式切换：胶囊 tab（选中项 primary 10% 底 + matchedGeometry 动画），放不下时降级为下拉菜单 [已证实]。
- 表单：`Form`/`FormView`，行式 Section，右侧值、右侧 Picker/TextField 对齐 [已证实]。

### 4.5 字体 / 密度 / 图标
- 字体：系统字体（SF）；标题 `.headline`、正文 `.subheadline`/`.body`、说明 `.caption`、大数值 `.title2`（medium 字重）；数字用 `.monospacedDigit()` [已证实]。
- 列表密度：连接/日志紧凑列表；Groups 自适应网格 itemMinWidth **170**（宽屏多列）[已证实]。
- 图标集：**SF Symbols**（全 app 统一）。关键图标：dashboard=`text.and.command.macwindow`、groups=`rectangle.3.group.fill`、connections=`list.bullet.rectangle.portrait.fill`、logs=`list.bullet.rectangle`、tools=`terminal.fill`、settings=`gear.circle.fill`、start=`play.fill`、stop=`stop.fill`、urltest=`bolt.fill`、share=`square.and.arrow.up`、qrcode=`qrcode`/`qrcode.viewfinder`、edit=`pencil`、update=`arrow.clockwise`、memory/gardening 等见各卡 header（如 status=`info.circle.fill`、connections=`link.circle.fill`、upload=`arrow.up.circle.fill`、download=`arrow.down.circle.fill`、httpProxy=`network`、clashMode=`circle.grid.2x2.fill`、profile=`doc.text.fill`）[已证实]。
  > Windows 无 SF Symbols：建议用 Fluent Icons / 自绘同风格线性图标，保持「图标+文字」左对齐 + 侧栏图形一致的观感。

### 4.6 交互反馈
- Alert 统一 `AlertState`（错误：action + error.localizedDescription）[已证实]。
- 删除 destructive 红色；清空缓存/销毁目录红色按钮 [已证实]。
- 语言切换提示「重启后生效」[已证实]。

---

## 5. 参考链接清单（实际打开/读取）

- 仓库目录树（dev 分支，全量 463 blobs）：`https://api.github.com/repos/SagerNet/sing-box-for-apple/git/trees/dev?recursive=1`
- README：`https://raw.githubusercontent.com/SagerNet/sing-box-for-apple/main/README.md`
- `ApplicationLibrary/Views/NavigationPage.swift` — 页面枚举
- `ApplicationLibrary/Views/SidebarView.swift` — 侧栏/可见性/badge
- `ApplicationLibrary/Views/Dashboard/DashboardView.swift`、`ActiveDashboardView.swift`、`Overview/OverviewView.swift` — Dashboard 组装
- `ApplicationLibrary/Views/Dashboard/Cards/DashboardCard.swift`、`DashboardCardView.swift`、`CardManagementSheet.swift`、`DashboardCardConfiguration.swift`、`StatusCard/ConnectionsCard/UploadTrafficCard/DownloadTrafficCard/HTTPProxyCard/ClashModeCard/TrafficLineChart/ProfileCard/ProfilePickerSheet.swift` — 卡片内容/样式/管理
- `ApplicationLibrary/Views/Groups/GroupListView.swift`、`GroupView.swift`、`GroupItemView.swift`、`GroupListViewModel.swift` — 策略组
- `ApplicationLibrary/Views/Connections/ConnectionListView.swift` — 连接列表/筛选/全关
- `ApplicationLibrary/Views/Log/LogView.swift`、`ANSIColors.swift` — 日志
- `ApplicationLibrary/Views/Profile/NewProfileMenuView.swift`、`ImportProfileView.swift`、`NewProfileView.swift`、`EditProfileView.swift`、`ProfileType+Presentation.swift` — 配置导入/新建/编辑
- `ApplicationLibrary/Views/Setting/SettingView.swift`、`CoreView.swift`、`MacAppView.swift`、`PacketTunnelView.swift`、`ProfileOverrideView.swift` — 设置分页
- `ApplicationLibrary/Views/Tools/ToolsView.swift`、`TailscaleEndpointView.swift` — Tools/端点
- `ApplicationLibrary/Views/Abstract/ViewModifiers.swift`（cardStyle）、`Library/Shared/Color+Extension.swift` — 样式/配色
- `Library/Database/Profile.swift`、`ProfileManager.swift`、`SharedPreferences.swift` — 数据模型/存储
- `Library/Network/CommandClient.swift`、`OutboundGroup.swift`、`ExtensionProfile.swift`、`ExtensionProvider.swift`、`ExtensionStartOptions.swift` — 内核交互/clash 命令
- `Library/Shared/AppConfiguration.swift`、`FilePath.swift`、`URL+SchemeAction.swift` — 路径/URL scheme
- `MacLibrary/MainView.swift`、`MacApplication.swift` — 窗口/分栏/onOpenURL
- `SFM/Info.plist` — URL scheme(`sing-box`)、`.bpf` 文档类型、iCloud
- 官方文档：`https://sing-box.sagernet.org/`（注：`/installation/clients/sfm/`、`/sfi/` 当前返回 404，正文以源码为准）

---

## 附录：最容易被忽略、但 SFM 确实有的功能
1. **Endpoint 原样透传 + 端点状态页**：配置里的 `endpoints`（尤其 **Tailscale endpoint**）必须完整交给内核，并在 UI 呈现 State/退出节点/Peer 列表 —— 生成 config.json 时严禁裁剪 `endpoints`。
2. **Dashboard 卡片可自定义增删+拖拽排序**（含 Reset），且 Profile 卡强制常驻。
3. **Profile Override 的隐式配置改写**：Hide VPN Icon / No Default Route / Exclude APNs Route 会在启动时**向 route_exclude_address、auto_route、bypass_domain 注入内容**，并在运行中热重启服务生效（不只是 UI 开关）。