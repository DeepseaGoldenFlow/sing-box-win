# Windows native preview · Phase 1

`SingBoxWin.Native/` is an unpackaged, self contained WinUI 3 / .NET 8 x64 client. The Go engine is unchanged. Build and package with `powershell -ExecutionPolicy Bypass -File scripts/build-native.ps1` from Windows. Put the prebuilt Go engine at `build/sing-box-win.exe` first; the script copies it into `build/native/` alongside the client and creates `build/native.zip`. The .NET 8 SDK must be on PATH. Launch `build/native/SingBoxWin.Native.exe`.

Implemented pages: dashboard (live state, core controls, upload/download, rolling 60 point plot and totals), profiles (URL import, enable/disable/delete), tools (Tailscale status and connectivity test, four placeholders), settings (engine preferences, immediate language/theme switching and native close behavior). Connections and logs are basic page skeletons with working lists, close all, level filter and auto scroll. No advanced connection actions, log search/export or profile context menu in this phase.

The default close action hides the native window and leaves the engine running. Launching the executable again signals the primary instance to show and activate the existing window. Explicit Quit, or Close when “Quit when window closes” is enabled, calls the engine's quit endpoint before exiting. The native close preference alone is stored in `%APPDATA%/sing-box-win/native-preferences.json`. Engine settings remain owned by the engine. Its existing `auto_start` behavior is preserved: the Go executable is registered at sign-in, not the native executable.

## API contract

Verified against `cmd/singbox-win/main.go`, `client.go`, `tools.go` before implementation. Connection response fields were additionally verified against the pinned `github.com/sagernet/sing-box v1.14.4`, `experimental/clashapi/connections.go` (`connectionObject.MarshalJSON` and `connectionsSnapshot`). No guessed routes or fields:

| Method / source | Fields consumed or submitted |
| --- | --- |
| `%APPDATA%/sing-box-win/runtime.json` | `url`, `pid` (engine process metadata); only a loopback HTTP URL is accepted |
| GET `/api/state` | `running`, `starting`, `profile`, `system_proxy`, `error`; `starting` is true while the engine brings the core up off the request path, so the client shows Starting instead of Stopped |
| GET `/api/native/startup` | `marks[]` (`node`, `ms`, `at`), `core_started_at`; every stage is logged once as a `startup-timing` record |
| POST `/api/native/ui-ready` | request `at_unix_ms`, `elapsed_ms`, `phase`; the window reports its first painted frame so `engine.log` shows whether it preceded the core. Response: `ui_ms`, `core_ms`, `window_before_core` |
| POST `/api/core/start`, `/api/core/stop`, `/api/core/restart` | no request body; HTTP success or raw error body |
| GET `/api/traffic` | `upload`, `download`, `upload_total`, `download_total`, `connections`; requested only while core runs |
| GET `/api/profiles` | array or null: `id`, `name`, `type`, `updated`, `enabled`; subscription `url` is not rendered or persisted by the frontend |
| POST `/api/profiles/import` | request `name`, `url`; response profile unused |
| PUT `/api/profiles/{id}` | request `enabled`; HTTP success or error |
| DELETE `/api/profiles/{id}` | no body; HTTP success or error |
| GET `/api/connections` | `connections[]`: `metadata.host`, `metadata.destinationIP`, `metadata.network`, `upload`, `download`, `start` |
| DELETE `/api/connections` | no body; Go wraps the upstream 204 as JSON null |
| GET `/api/logs?limit=500&level=…` | array: `time`, `level`, `message`; empty level means all |
| GET `/api/tools/tailscale` | `enabled`, `registered`, `hostname`, `ips[]`, `key_expiry`, `last_sync` (cache sync, not handshake); missing fields render an em dash with an explanation; `state_dir_exists` is defined by Go but not needed by UI |
| POST `/api/tools/tailscale/test` | no body; `ok`, `status`, `ms`, `error`; render failure even when outer HTTP response is 200 |
| GET `/api/settings` | `auto_start`, `auto_connect`, `port`, `language`, `theme` |
| POST `/api/settings` | partial update: `auto_start`, `auto_connect`, `port`, `language`, `theme`; Go preserves `update_hours`, `allow_lan`, profiles and proxy backup |
| POST `/api/proxy` | request `enabled` |
| POST `/api/quit` | no body; success before native application exits |

No subscription URLs or tokens are written to logs or committed. UI errors are displayed in an InfoBar; the connectivity result displays the engine's original error as requested.

## Validation / handoff

P4: Windows Release self-contained x64 build passed with zero warnings and zero errors. Packaging produced 445 files including the Go engine. XML/resource checks and `git diff --check` also pass. WindowsAppSDK 1.6 and Windows SDK BuildTools remain pinned; no packages were added. The tools page uses real engine data and shows HTTP status and milliseconds for both successful and failed tests. The current API supplies no handshake timestamp, so that field displays an em dash with an explanation. Unhandled errors go to `%APPDATA%/sing-box-win/native-error.log`; trace logging has been removed.

On Windows, run `powershell -ExecutionPolicy Bypass -File scripts/build-native.ps1`, then verify: cold start without runtime.json; reconnect with existing engine; error/retry when engine is absent; start/stop/restart and no-profile disabled state; real traffic plotting; URL import and engine rejection while switching a running profile; Tailscale enabled/unregistered/registered plus 404 fallback and failed test; settings persistence, Chinese/English switch, light/dark switch; background close followed by a second launch; explicit quit; connection close all; log filtering with auto scroll disabled. Confirm 1040×720 initial size, 900×620 minimum, title dragging/caption buttons and actual macOS-like visual fit at 100%/150% DPI.

Startup ordering (requirement C): the window is created and activated before `ConnectAsync` runs, and `ConnectAsync` is now deferred until `Root.Loaded` has fired, so the first frame is painted before engine discovery begins. Verify on Windows with `%APPDATA%\sing-box-win\engine.log`: the `ui-first-frame` record must precede `core-started` (or exist before the core is up), and `POST /api/native/ui-ready` must answer `window_before_core:true` on a cold start.
