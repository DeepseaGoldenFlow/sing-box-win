# v0.5.0 Windows 11 验证与 SFM 对照

2026-11-10，在用户实际 Windows 11 桌面会话（Session 1）部署并验证。

## 原问题和修复

v0.4.2 内核可用但系统代理关闭；手工指定 2085 能访问 Google，浏览器默认并未使用此代理。原生 UI 没有暴露系统代理开关，也没有托盘。v0.5.0 默认服务启动即开启系统代理，提供仪表开关、明确状态提示及 Google 连通性测试。通过关闭自动代理偏好可保留手动使用方式。

原生托盘使用 Shell_NotifyIcon 与 WinUI HWND 子类回调；Explorer 重启后重新注册。默认关闭窗口只隐藏，点击托盘恢复，右键提供服务/代理/退出操作。图标失败时保留窗口。

## 与本机 SFM 对照

本机应用标识 io.nekohasekai.sfamt.standalone，实际检查仪表、组、应用设置和隧道设置。

| 项目 | Windows v0.5.0 |
|---|---|
| 仪表、节点组、连接、日志、工具、设置导航 | 已有 |
| 上传下载、内存与连接数、图表 | 已有，补齐双列卡片 |
| 菜单栏/托盘后台常驻 | 本次补齐托盘、关闭隐藏、点击恢复与快捷菜单 |
| 系统网络接管 | 补齐系统代理控制和启动自动开启；尚无 TUN |
| 自动连接与登录后台启动 | 补齐设置入口和原生客户端自启路径 |
| 配置导入、切换、更新 | 已有后端，补齐当前配置更新按钮 |
| Tailscale 元数据/测试 | 已有；未证明实际 tailnet 连通 |
| 隧道全部流量、路由排除等 | 待 Windows 管理员权限、Wintun 与 TUN 实现 |
| 应用/核心更新、按需启动、远程控制 | 尚未完整实现 |
| 图形规则编辑、配置编辑和更完整分享管理 | 尚未完整实现 |

Apple Network Extension 的隧道开关不能直接照搬为 Windows 功能；当前系统代理仅覆盖遵循 Windows 代理设置的应用。

## 验证

- Windows amd64 Go 引擎构建、Linux/Windows go vet、internal/config 与 cmd/singbox-win 测试通过。
- 原生 WinUI Release/x64/self-contained 构建通过；使用已安装 Visual Studio MSBuild，dotnet build 缺少 PRI packaging task，构建脚本已修正。
- Windows Edge 临时资料目录、未指定 --proxy-server，访问 Google 页面得到标题 Google。
- 实机 API：version=0.5.0，running=true，system_proxy=true，tun=false，error 为空。
- Google generate_204 通过本机代理返回 204。
- scripts/test-windows-smoke.ps1 在交互桌面验证 Explorer 托盘注册、真实 WM_CLOSE 隐藏后服务存活、托盘回调恢复窗口、服务停止释放代理、再次启动开启代理及 Google HTTP 204。
- 未进行整机重启、Explorer 重启、长期稳定性或 TUN 验证。托盘右键菜单代码已构建，但没有人工逐项点击验收。

桌面安装路径保持 C:/Users/giraf/Desktop/sing-box-for-Windows/app。原 v0.4.2 保存在 %LOCALAPPDATA%/sing-box-win-backups/20261110-210206/app。用户订阅和认证信息未收入仓库。
