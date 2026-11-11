# Windows 深链与文件关联清单

本文件是 `internal/platform/platform_registry_windows.go` 的注册表清单，包含安装时写入、
卸载时必须清理的全部项。实现只使用 `HKCU`（当前用户），不需要管理员权限。

## 1. URL 协议

| 键 | 值 |
| --- | --- |
| `HKCU\Software\Classes\sing-box` | `(默认)` = `URL:sing-box`；`URL Protocol` = 空；`FriendlyTypeName` = `URL:sing-box` |
| `HKCU\Software\Classes\sing-box\shell\open\command` | `(默认)` = `"<sing-box-win.exe>" "%1"` |
| `HKCU\Software\Classes\singboxwin` | `(默认)` = `URL:sing-box-win`；`URL Protocol` = 空；`FriendlyTypeName` = `URL:sing-box-win` |
| `HKCU\Software\Classes\singboxwin\shell\open\command` | `(默认)` = `"<sing-box-win.exe>" "%1"` |

`sing-box` 是上游标准 scheme（`sing-box://import-remote-profile?url=<encoded>#<name>`），
`singboxwin` 是历史 scheme（`singboxwin://install-config?url=<encoded>`），两者都保留。
命令指向 Go 引擎而不是 WinUI 客户端：引擎独占 profile 存储，并通过单实例通道把第二个
进程收到的深链/文件转交给已运行实例。

## 2. 文件关联

| 键 | 值 |
| --- | --- |
| `HKCU\Software\Classes\SingBoxWin.Profile` | `(默认)` = `sing-box Profile`；`FriendlyTypeName` = `sing-box 配置文件` |
| `HKCU\Software\Classes\SingBoxWin.Profile\shell\open\command` | `(默认)` = `"<sing-box-win.exe>" "%1"` |
| `HKCU\Software\Classes\.bpf` | `(默认)` = `SingBoxWin.Profile` |
| `HKCU\Software\Classes\.json\OpenWithProgids` | 值名 `SingBoxWin.Profile` = 空字符串 |

`.bpf` 是无歧义的 sing-box 配置扩展名，注册为默认处理程序。`.json` **不**抢占默认处理
程序——那会劫持系统上所有无关的 JSON 文档；只写入 `OpenWithProgids`，让客户端出现在
资源管理器的「打开方式」里，由用户自行选择。

## 3. 卸载恢复

首次注册前把原始状态记录到 `HKCU\Software\sing-box-win\Backup`：

| 值名 | 类型 | 含义 |
| --- | --- | --- |
| `recorded` | DWORD | 快照已写入（重复开启不会覆盖原始快照） |
| `bpf_key_existed` | DWORD | 注册前 `.bpf` 键是否已存在 |
| `bpf_had_default` | DWORD | 注册前 `.bpf` 是否已有默认处理程序 |
| `bpf_default` | SZ | 被覆盖的原始 `.bpf` 默认处理程序 |
| `json_key_existed` | DWORD | 注册前 `.json\OpenWithProgids` 键是否已存在 |

卸载（`POST /api/protocol {"enabled":false}`）时：

1. 删除 `.json\OpenWithProgids` 下的 `SingBoxWin.Profile` 值，**不动**其它应用的值；
   若该键是本程序创建的且已为空，才删除该键。
2. `.bpf`：有旧处理程序则恢复；否则仅在自己的值仍是 `SingBoxWin.Profile` 时清空，
   并且只删除本程序创建、现已为空的 `.bpf` 键。
3. 删除 `SingBoxWin.Profile` 与两个 scheme 键，最后删除 `Backup` 快照。

## 4. 验证

在 Windows 交互式用户会话中：

```powershell
# 1. 注册（等同设置页开关）。CMP-41 起所有 /api/* 都要求 bearer token，
#    从 ACL 保护的运行目录读取；不要把 token 写入脚本或日志。
. .\scripts\sfw-runtime.ps1
$sfw = Get-SfwRuntime
Invoke-RestMethod -Method Post -Uri "$($sfw.Url)/api/protocol" -Headers (Get-SfwHeaders $sfw) -ContentType 'application/json' -Body '{"enabled":true}'

# 2. 自动验收：键值、.json 默认处理程序未被改动、双击 .bpf 真正导入、
#    非法容器不新增配置且留下可见错误、卸载后只删除本程序的键
pwsh -File scripts\test-windows-associations.ps1 -EngineExe build\native\sing-box-win.exe
```

`-KeepRegistration` 可跳过最后的卸载步骤，便于人工继续测试深链。
