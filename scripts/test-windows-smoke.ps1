# Run in the interactive user's Windows session after installing the native client.
# Exercises the real close notification, tray callback and engine API.
param([string]$ResultPath = "$env:TEMP\singbox-smoke-result.json")
$ErrorActionPreference = 'Stop'
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
    $r=Get-Content "$env:APPDATA\sing-box-win\runtime.json" -Raw | ConvertFrom-Json
    [SingBoxSmoke]::PostMessage($h,0x10,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    Start-Sleep -Milliseconds 700
    if([SingBoxSmoke]::IsWindowVisible($h)) { throw 'Close did not hide window' }
    if(!(Get-Process -Id $ui.Id -ErrorAction SilentlyContinue)) { throw 'Close exited client' }
    if(!(Invoke-RestMethod ($r.url+'/api/state')).running) { throw 'Close stopped service' }
    $results.close_hides_and_service_survives=$true
    [SingBoxSmoke]::PostMessage($h,0x802a,[IntPtr]1,[IntPtr]0x202) | Out-Null
    Start-Sleep -Milliseconds 700
    if(![SingBoxSmoke]::IsWindowVisible($h)) { throw 'Tray click did not restore window' }
    $results.tray_restores_window=$true
    Invoke-RestMethod ($r.url+'/api/core/stop') -Method Post | Out-Null
    $stopped=Invoke-RestMethod ($r.url+'/api/state')
    if($stopped.running -or $stopped.system_proxy) { throw 'Stop did not release system proxy' }
    $results.stop_releases_proxy=$true
    Invoke-RestMethod ($r.url+'/api/core/start') -Method Post | Out-Null
    $state=Invoke-RestMethod ($r.url+'/api/state')
    $proxy=Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
    if(!$state.running -or !$state.system_proxy -or $proxy.ProxyEnable -ne 1 -or $proxy.ProxyServer -ne '127.0.0.1:2085') { throw 'Start did not enable working system proxy' }
    $results.start_enables_proxy=$true
    $test=Invoke-RestMethod ($r.url+'/api/tools/connectivity') -Method Post
    if(!$test.ok) { throw ('Google connectivity failed: '+$test.error) }
    $results.google_http=$test.status
    $results.version=$state.version
    $results.ok=$true
} catch { $results.ok=$false; $results.error=$_.Exception.Message }
$results | ConvertTo-Json | Set-Content $ResultPath -Encoding UTF8
if(!$results.ok) { exit 1 }
