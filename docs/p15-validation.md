# P1.5 验收（2026-11-10）

使用 Go 1.26.10 / sing-box 1.14.4，CGO_ENABLED=0，构建标签保持 `with_gvisor,with_quic,with_utls,with_clash_api`。
指定 `/opt/data/tools` 在本环境缺失，建立指向已有 `/data/stacks/hermes-main/data/tools` 的符号链接后使用原构建环境，无降版本。

## 构建、vet、测试输出

```text
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/sing-box-win.exe ./cmd/singbox-win
build/sing-box-win.exe 82569216 bytes
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/sbw-linux ./cmd/singbox-win
/tmp/sbw-linux 84389560 bytes
CGO_ENABLED=0 GOOS=linux go vet ./...       # exit 0，无输出
CGO_ENABLED=0 GOOS=windows go vet ./...     # exit 0，无输出
CGO_ENABLED=0 GOOS=linux go test ./cmd/singbox-win ./internal/config
ok github.com/JARVIS/sing-box-win/cmd/singbox-win 0.112s
ok github.com/JARVIS/sing-box-win/internal/config 0.042s
```

## 真后端 + 无头 Chrome

使用隔离 APPDATA、合成 direct/block/selector 配置运行 Linux 二进制，不使用静态 HTTP 服务代替 API。

```text
curl /api/state
{"error":"","profile":"test","running":true,"system_proxy":false,"tun":false,"version":"0.2.0"}
DOM {"nav":5,"icons":5,"statusCard":true,"bad":false,"padding":"28px","font":"13px"}
i18n {"lang":"en","nav":["Dashboard","Profiles","Connections","Logs","Settings"],"title":"Settings"}
profiles 2
network/browser errors []
UI ASSERTIONS PASS
```

还断言了保存语言后刷新仍为 `en`。使用临时订阅 HTTP 端点实测二次启动深链导入：`LINUX secondary deep-link import PASS profiles=2 same_pid=true`。关键可见文案不含 `404`、U+FFFD 或 `□`。
截图：[dashboard.png](screenshots/dashboard.png)，真实运行中，使用英文展示避免容器中文字体缺失。
复现：以 `--headless --no-sandbox --remote-debugging-port=19222` 启动 Chrome，在隔离数据目录运行后端并启动内核，执行 `node docs/ui-check.mjs http://127.0.0.1:<UI-port>`（Node 原生 WebSocket，无 npm 依赖）。脚本会把该测试实例语言保存为英文并生成截图。

## Windows 原生执行输出

通过 WSL 调用 Windows PowerShell 5.1 启动本次交叉编译 exe。使用独立临时 APPDATA 和 mixed 22085，未操作现有安装软件。代理测试前保留原值，重启后比较 ProxyEnable/ProxyServer，输出不暴露原代理值。

```text
WINDOWS state running=True profiles=1 groups=2 selector_members=2
WINDOWS node switch PASS
WINDOWS single instance PASS
WINDOWS forced termination + proxy recovery PASS
WINDOWS stopped=True
```

没有把本机配置、真实订阅、控制器密钥或凭据写入仓库。托盘采用 README 中的常驻/任务栏窗口替代方案；TUN、规则编辑与内核更新仍在 P2。
