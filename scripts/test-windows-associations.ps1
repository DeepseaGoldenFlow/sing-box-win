# Verify sing-box:// + singboxwin:// URL schemes and .bpf/.json file association.
# Run in the interactive user's Windows session (HKCU, no admin needed).
#   1. Register:   Invoke-RestMethod -Method Post -Body '{"enabled":true}' -ContentType application/json "$engine/api/protocol"
#   2. This script checks the keys, then imports a real .bpf through the same
#      path Explorer uses (ShellExecute -> engine -> /api/profiles).
#   3. It then unregisters and asserts only our own keys/values are gone.
param(
    [Parameter(Mandatory = $true)][string]$EngineExe,
    [string]$ResultPath = "$env:TEMP\singbox-associations-result.json",
    [switch]$KeepRegistration
)
$ErrorActionPreference = 'Stop'
$results = [ordered]@{ engine = $EngineExe; checks = @() }
function Check([string]$name, [bool]$ok, [string]$detail = "") {
    $results.checks += [ordered]@{ name = $name; pass = $ok; detail = $detail }
    if (-not $ok) { $script:failed = $true }
    Write-Host ("{0} {1} {2}" -f ($(if ($ok) { 'PASS' } else { 'FAIL' })), $name, $detail)
}
$failed = $false
$classes = 'HKCU:\Software\Classes'
$backup = 'HKCU:\Software\sing-box-win\Backup'
# Keys this client owns and must remove on uninstall. .json is deliberately not
# here: we only add one value inside it, which must be removed without touching
# other applications' entries.
$ourKeys = @(
    "$classes\sing-box",
    "$classes\sing-box\shell\open\command",
    "$classes\singboxwin",
    "$classes\singboxwin\shell\open\command",
    "$classes\SingBoxWin.Profile",
    "$classes\SingBoxWin.Profile\shell\open\command",
    $backup
)
function OurKeysPresent { ($ourKeys | Where-Object { Test-Path $_ }).Count }

# Snapshot the pre-existing state so cleanup can be compared against it.
$bpfExisted = Test-Path "$classes\.bpf"
$origBPFDefault = (Get-ItemProperty "$classes\.bpf" -ErrorAction SilentlyContinue).'(default)'
$origJSONValues = @()
if (Test-Path "$classes\.json\OpenWithProgids") {
    $origJSONValues = @((Get-ItemProperty "$classes\.json\OpenWithProgids").PSObject.Properties |
        Where-Object { $_.Name -notin 'PSPath', 'PSParentPath', 'PSChildName', 'PSDrive', 'PSProvider' } |
        ForEach-Object { $_.Name } | Sort-Object)
}

# --- Register through the engine API, the same call the UI uses -------------
$runtime = Get-Content "$env:APPDATA\sing-box-win\runtime.json" -Raw | ConvertFrom-Json
$base = $runtime.url
Invoke-RestMethod -Method Post -Uri "$base/api/protocol" -ContentType 'application/json' -Body '{"enabled":true}' | Out-Null

Check 'standard scheme registered' (Test-Path "$classes\sing-box") 'sing-box://'
Check 'legacy scheme registered' (Test-Path "$classes\singboxwin") 'singboxwin://'
Check 'standard scheme URL Protocol flag' ((Get-ItemProperty "$classes\sing-box" -Name 'URL Protocol' -ErrorAction SilentlyContinue) -ne $null)
$stdCmd = (Get-ItemProperty "$classes\sing-box\shell\open\command").'(default)'
Check 'standard scheme command points at engine' ($stdCmd -like "*$([IO.Path]::GetFileName($EngineExe))*`"%1`"*") $stdCmd
Check '.bpf default handler' ((Get-ItemProperty "$classes\.bpf").'(default)' -eq 'SingBoxWin.Profile')
Check '.json OpenWith entry' ((Get-ItemProperty "$classes\.json\OpenWithProgids").PSObject.Properties.Name -contains 'SingBoxWin.Profile')
$jsonDefault = (Get-ItemProperty "$classes\.json" -ErrorAction SilentlyContinue).'(default)'
Check '.json default handler untouched' ($jsonDefault -ne 'SingBoxWin.Profile') "default=$jsonDefault"
Check 'progID command' ((Get-ItemProperty "$classes\SingBoxWin.Profile\shell\open\command").'(default)' -like "*$([IO.Path]::GetFileName($EngineExe))*`"%1`"*")

# --- Real import through Explorer's association path -----------------------
$tmp = Join-Path $env:TEMP ('sfw-assoc-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$bpf = Join-Path $tmp 'assoc-test.bpf'
# Build a real BPF container with the engine's own exporter, then import it.
$profiles = Invoke-RestMethod -Uri "$base/api/profiles"
if ($profiles.Count -eq 0) {
    Invoke-RestMethod -Method Post -Uri "$base/api/profiles/import" -ContentType 'application/json' `
        -Body (@{ name = 'assoc-seed'; content = '{"outbounds":[{"type":"direct","tag":"direct"}]}' } | ConvertTo-Json) | Out-Null
    $profiles = Invoke-RestMethod -Uri "$base/api/profiles"
}
$seedId = $profiles[0].id
Invoke-WebRequest -Uri "$base/api/profiles/$seedId/export?format=bpf" -OutFile $bpf | Out-Null
Check 'BPF export produced bytes' ((Get-Item $bpf).Length -gt 4) "$((Get-Item $bpf).Length) bytes"

$before = (Invoke-RestMethod -Uri "$base/api/profiles").Count
Start-Process -FilePath $bpf -Wait
Start-Sleep -Milliseconds 800
$after = (Invoke-RestMethod -Uri "$base/api/profiles").Count
Check 'double-click .bpf imported a profile' ($after -eq $before + 1) "$before -> $after"

# Wrong container must fail loudly and add nothing.
$bad = Join-Path $tmp 'bad.bpf'
Set-Content -Path $bad -Value 'not a bpf container' -Encoding ascii
Start-Process -FilePath $bad -Wait
Start-Sleep -Milliseconds 800
$state = Invoke-RestMethod -Uri "$base/api/state"
$unchanged = (Invoke-RestMethod -Uri "$base/api/profiles").Count -eq $after
Check 'invalid BPF adds no profile' $unchanged
Check 'invalid BPF leaves a visible error' ($state.error -ne '') $state.error

# --- Unregister and assert cleanup ----------------------------------------
if (-not $KeepRegistration) {
    Invoke-RestMethod -Method Post -Uri "$base/api/protocol" -ContentType 'application/json' -Body '{"enabled":false}' | Out-Null
    $left = OurKeysPresent
    Check 'uninstall removes every registry key we own' ($left -eq 0) "$left key(s) left"
    # .json: only our value goes away; the key and every other app's value stay.
    Check '.json key preserved after cleanup' (Test-Path "$classes\.json") ''
    $jsonNow = @()
    if (Test-Path "$classes\.json\OpenWithProgids") {
        $jsonNow = @((Get-ItemProperty "$classes\.json\OpenWithProgids").PSObject.Properties |
            Where-Object { $_.Name -notin 'PSPath', 'PSParentPath', 'PSChildName', 'PSDrive', 'PSProvider' } |
            ForEach-Object { $_.Name } | Sort-Object)
    }
    $origOthers = @($origJSONValues | Where-Object { $_ -ne 'SingBoxWin.Profile' })
    $missing = @($origOthers | Where-Object { $jsonNow -notcontains $_ })
    Check '.json other OpenWith entries preserved' ($missing.Count -eq 0) `
        "missing=$($missing -join ',') before=$($origJSONValues -join ',') after=$($jsonNow -join ',')"
    Check 'our .json OpenWith value removed' ($jsonNow -notcontains 'SingBoxWin.Profile')
    # .bpf: a pre-existing handler is restored; a key we created is not left behind.
    if ($bpfExisted) {
        $restored = (Get-ItemProperty "$classes\.bpf" -ErrorAction SilentlyContinue).'(default)'
        Check 'pre-existing .bpf handler restored' ($restored -eq $origBPFDefault) "before=$origBPFDefault after=$restored"
    } else {
        Check 'created .bpf association removed' ((Get-ItemProperty "$classes\.bpf" -ErrorAction SilentlyContinue).'(default)' -ne 'SingBoxWin.Profile')
    }
}
Remove-Item -Recurse -Force $tmp
$results.pass = -not $failed
$results | ConvertTo-Json -Depth 5 | Set-Content -Path $ResultPath -Encoding utf8
if ($failed) { Write-Error "Association checks failed; see $ResultPath"; exit 1 }
Write-Host "All association checks passed. Result: $ResultPath"
