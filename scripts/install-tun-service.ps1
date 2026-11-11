# Install/upgrade/remove the SFW TUN broker service.
#
# DELIVERABLE ONLY for P0: this script must be run by the operator (JARVIS) on an
# isolated, approved test machine, as an administrator, against a signed test
# package. It must never be run against a real Desktop, a repo/build folder, a
# Downloads folder, or a user-writable directory (docs/proposal-tun-windows.md
# §0.1: "禁止将 Desktop、Downloads、repo/build、用户可替换的 exe 注册为最高权限常驻任务").
#
# The P0 broker is dry-run only: it answers ping/version/hello and a dry-run
# recover. This script does not create a TUN adapter, route, DNS entry or host.
#
#   # install from an approved package directory (contains signed binaries)
#   .\install-tun-service.ps1 -PackageRoot 'D:\sfw-pkg-0.6.0-p0' -RequireAdministrator
#   # remove
#   .\install-tun-service.ps1 -Uninstall -RequireAdministrator
param(
    [string]$PackageRoot = '',
    [switch]$Uninstall,
    [switch]$RequireAdministrator,
    [string]$ServiceName = 'SFWTunBroker',
    [string]$InstallDir = 'C:\Program Files\SFW\Core'
)
$ErrorActionPreference = 'Stop'

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (!$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'This operation requires an elevated administrator. It must be run only on the approved test machine.'
    }
}
function Assert-NotUserWritable([string]$path) {
    $full = [IO.Path]::GetFullPath($path)
    $blocked = @($env:USERPROFILE, $env:TEMP, $env:LOCALAPPDATA, $env:APPDATA,
        'C:\Users', 'C:\Windows\Temp')
    foreach ($prefix in $blocked) {
        if ($prefix -and $full.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Refusing to install a privileged service from a user-writable path: $full"
        }
    }
    # A repo/build checkout has no publisher signature; the hash+signature check below
    # would fail anyway, but reject the path early so the intent is explicit.
    if ($full -match '\\(build|bin|obj|\.git)(\\|$)') {
        throw "Refusing to install from a build/checkout path: $full"
    }
}
function Assert-SignedBinary([string]$path) {
    if (!(Test-Path -LiteralPath $path)) { throw "Missing package file: $path" }
    $sig = Get-AuthenticodeSignature -LiteralPath $path
    if ($sig.Status -ne 'Valid') { throw "Signature is not valid for $path ($($sig.Status)). Use an approved signed build." }
    return (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
}
function Assert-SafePathInPackage([string]$file) {
    $name = [IO.Path]::GetFileName($file)
    if ($name -ne 'sfw-tun-broker.exe') { throw "Only sfw-tun-broker.exe may be copied; got $name" }
}

if ($RequireAdministrator) { Assert-Administrator }

if ($Uninstall) {
    if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        & sc.exe delete $ServiceName | Out-Null
    }
    Write-Host "Removed $ServiceName. Binaries under $InstallDir are left for the installer to manage."
    return
}

if (!$PackageRoot) { throw '-PackageRoot is required for install.' }
if (!(Test-Path -LiteralPath $PackageRoot)) { throw "Package root not found: $PackageRoot" }
Assert-NotUserWritable $PackageRoot
Assert-NotUserWritable $InstallDir

# --- validate the package before touching Program Files --------------------
$brokerSource = Join-Path $PackageRoot 'sfw-tun-broker.exe'
Assert-SafePathInPackage $brokerSource
$hash = Assert-SignedBinary $brokerSource
Write-Host "Broker SHA256: $hash"
$version = (Get-Item -LiteralPath $brokerSource).VersionInfo.FileVersion
if (!$version) { throw 'Package has no file version; refusing to install an unversioned privileged binary.' }

# --- protected install directory -----------------------------------------
if (!(Test-Path -LiteralPath $InstallDir)) { New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null }
# SYSTEM + Administrators write, Users read/execute only; no user-writable grant.
$acl = Get-Acl -LiteralPath $InstallDir
$acl.SetAccessRuleProtection($true, $false)
foreach ($rule in @($acl.Access)) { $acl.RemoveAccessRule($rule) | Out-Null }
foreach ($entry in @(
        @{ Id = 'NT AUTHORITY\SYSTEM'; Rights = 'FullControl' },
        @{ Id = 'BUILTIN\Administrators'; Rights = 'FullControl' },
        @{ Id = 'BUILTIN\Users'; Rights = 'ReadAndExecute' })) {
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new(
            $entry.Id, $entry.Rights, 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
}
Set-Acl -LiteralPath $InstallDir -AclObject $acl

$brokerTarget = Join-Path $InstallDir 'sfw-tun-broker.exe'
Copy-Item -LiteralPath $brokerSource -Destination $brokerTarget -Force
$targetHash = (Get-FileHash -LiteralPath $brokerTarget -Algorithm SHA256).Hash
if ($targetHash -ne $hash) { Remove-Item -LiteralPath $brokerTarget -Force; throw 'Copied broker hash differs from the approved package.' }
if ((Get-AuthenticodeSignature -LiteralPath $brokerTarget).Status -ne 'Valid') { throw 'Installed broker signature is not valid.' }

# --- service registration -------------------------------------------------
# DEMAND_START: no high-privilege process at boot until TUN is requested.
# Quoted binary path prevents unquoted-path hijacking.
$binaryPath = '"' + $brokerTarget + '"'
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
    Stop-Service -Name $ServiceName -Force
    & sc.exe config $ServiceName binPath= $binaryPath start= demand obj= 'LocalSystem' | Out-Null
} else {
    & sc.exe create $ServiceName binPath= $binaryPath start= demand obj= 'LocalSystem' DisplayName= 'SFW TUN Broker (dry-run)' | Out-Null
}
& sc.exe description $ServiceName 'Narrow dry-run broker for SFW TUN. P0: no adapter, route or DNS changes.' | Out-Null
# Only Administrators and SYSTEM may start/stop/configure the service; the
# interactive user interacts through the ACL-restricted pipe instead of the SCM.
& sc.exe sdset $ServiceName 'D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)' | Out-Null
& sc.exe start $ServiceName

Write-Host "Installed $ServiceName from $brokerTarget (version $version)."
Write-Host 'Verify: sc.exe qc SFWTunBroker ; sc.exe sdshow SFWTunBroker ; Get-Acl "HKLM:\SYSTEM\CurrentControlSet\Services\SFWTunBroker"'
Write-Host 'P0 reminder: this service must not create a TUN adapter, change routes or touch DNS.'
