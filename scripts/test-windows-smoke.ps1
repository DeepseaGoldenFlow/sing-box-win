# Run in the interactive user's Windows session after installing the native client.
# Exercises the real close notification, tray callback and engine API.
#
# Since CMP-41 (docs/proposal-tun-windows.md §0.2, §P0) every Engine API call
# needs the bearer token from the ACL-protected run directory. The script reads
# it from there and additionally asserts that the old bare requests fail with
# 401, so a regression to loopback-without-auth is caught here.
param([string]$ResultPath = "$env:TEMP\singbox-smoke-result.json")
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'sfw-runtime.ps1')
$results = [ordered]@{}
try {
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class SingBoxSmoke {
    public delegate bool EnumProc(IntPtr h, IntPtr p);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f, IntPtr p);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint id);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
    [StructLayout(LayoutKind.Sequential)] public struct Identifier { public uint size; public IntPtr hwnd; public uint id; public Guid guid; }
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int left, top, right, bottom; }
    [DllImport("shell32.dll")] static extern int Shell_NotifyIconGetRect(ref Identifier id, out Rect rect);
    public static bool HasTray(IntPtr h) { var id = new Identifier { size = (uint)Marshal.SizeOf<Identifier>(), hwnd = h, id = 1 }; Rect r; return Shell_NotifyIconGetRect(ref id, out r) == 0; }
    public static IntPtr Find(int pid) {
        IntPtr found = IntPtr.Zero;
        EnumWindows((h,p) => { uint id; GetWindowThreadProcessId(h,out id); if(id == pid && HasTray(h)) { found=h; return false; } return true; },IntPtr.Zero);
        return found;
    }
}
'@
    $ui=Get-Process SingBoxWin.Native | Select-Object -First 1
    $h=[SingBoxSmoke]::Find($ui.Id)
    if($h -eq [IntPtr]::Zero) { throw 'Native tray icon is not registered with Explorer' }
    $results.tray_registered=$true

    $runtime=Get-SfwRuntime

    # --- Authentication boundary (§0.2.4, §P0) ------------------------------
    # The token file must exist in the protected run directory; the old
    # %APPDATA%\sing-box-win\runtime.json must no longer exist.
    if(Test-Path "$env:APPDATA\sing-box-win\runtime.json") { throw 'Legacy unauthenticated runtime.json is still being published' }
    $noAuth = Invoke-SfwStatus -Uri ($runtime.Url+'/api/state')
    if($noAuth -ne 401) { throw "state without Authorization returned $noAuth, expected 401" }
    $wrongAuth = Invoke-SfwStatus -Uri ($runtime.Url+'/api/state') -Headers @{Authorization='Bearer definitely-wrong'}
    if($wrongAuth -ne 401) { throw "state with wrong token returned $wrongAuth, expected 401" }
    $noAuthWrite = Invoke-SfwStatus -Uri ($runtime.Url+'/api/core/stop') -Method Post
    if($noAuthWrite -ne 401) { throw "core/stop without Authorization returned $noAuthWrite, expected 401" }
    $results.state_requires_bearer=$true
    $results.write_api_requires_bearer=$true
    # A second launch must not create another engine or import anything.
    $engineCount=[int](Get-Process sing-box-win -ErrorAction SilentlyContinue | Measure-Object).Count
    if($engineCount -ne 1) { throw "expected exactly one engine process, found $engineCount" }
    $results.single_engine=$true
    $profilesBefore=@(Invoke-SfwAPI $runtime '/api/profiles').Count
    Start-Process -FilePath $ui.Path | Out-Null
    # Observe the second process appear, then wait for it to signal the primary
    # and exit. This proves activation actually ran instead of merely sleeping.
    $sawSecond=$false
    for($i=0;$i -lt 50;$i++) {
        $uiCount=[int](Get-Process SingBoxWin.Native -ErrorAction SilentlyContinue | Measure-Object).Count
        if($uiCount -gt 1) { $sawSecond=$true; break }
        Start-Sleep -Milliseconds 100
    }
    for($i=0;$i -lt 100;$i++) {
        $uiCount=[int](Get-Process SingBoxWin.Native -ErrorAction SilentlyContinue | Measure-Object).Count
        if($uiCount -eq 1) { break }
        Start-Sleep -Milliseconds 100
    }
    $profilesAfter=@(Invoke-SfwAPI $runtime '/api/profiles').Count
    if($sawSecond) { $results.second_instance_observed=$true }
    if($uiCount -ne 1) { throw "second launch left $uiCount UI processes instead of activating the existing window" }
    if($profilesAfter -ne $profilesBefore) { throw 'second launch imported a profile without an explicit request' }
    $results.second_launch_activates_only=$true

    [SingBoxSmoke]::PostMessage($h,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Start-Sleep -Milliseconds 700
    if([SingBoxSmoke]::IsWindowVisible($h)) { throw 'Close did not hide window' }
    if(!(Get-Process -Id $ui.Id -ErrorAction SilentlyContinue)) { throw 'Close exited client' }
    if(!(Invoke-SfwAPI $runtime '/api/state').running) { throw 'Close stopped service' }
    $results.close_hides_and_service_survives=$true
    [SingBoxSmoke]::PostMessage($h,0x802a,[IntPtr]1,[IntPtr]0x202) | Out-Null
    Start-Sleep -Milliseconds 700
    if(![SingBoxSmoke]::IsWindowVisible($h)) { throw 'Tray click did not restore window' }
    $results.tray_restores_window=$true
    Invoke-SfwAPI $runtime '/api/core/stop' -Method Post | Out-Null
    $stopped=Invoke-SfwAPI $runtime '/api/state'
    if($stopped.running -or $stopped.system_proxy) { throw 'Stop did not release system proxy' }
    $results.stop_releases_proxy=$true
    Invoke-SfwAPI $runtime '/api/core/start' -Method Post | Out-Null
    $state=Invoke-SfwAPI $runtime '/api/state'
    $proxy=Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
    if(!$state.running -or !$state.system_proxy -or $proxy.ProxyEnable -ne 1 -or $proxy.ProxyServer -ne '127.0.0.1:2085') { throw 'Start did not enable working system proxy' }
    $results.start_enables_proxy=$true
    $test=Invoke-SfwAPI $runtime '/api/tools/connectivity' -Method Post
    if(!$test.ok) { throw ('Google connectivity failed: '+$test.error) }
    $results.google_http=$test.status
    $results.version=$state.version
    $results.ok=$true
} catch { $results.ok=$false; $results.error=$_.Exception.Message }
# Never write the token or headers into the result file.
$results | ConvertTo-Json | Set-Content $ResultPath -Encoding UTF8
if(!$results.ok) { exit 1 }
