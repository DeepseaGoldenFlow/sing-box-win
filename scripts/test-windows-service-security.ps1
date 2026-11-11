# P0 security evidence for the authentication/permission boundary.
# docs/proposal-tun-windows.md §0.1-§0.3 and the §P0 "通过" list.
#
# Run in the interactive user's session. It changes nothing on the machine and
# never prints the engine token: checks report only pass/fail and token facts.
#
#   .\scripts\test-windows-service-security.ps1
#   .\scripts\test-windows-service-security.ps1 -BrokerPath 'C:\Program Files\SFW\Core\sfw-tun-broker.exe'
#
# Token facts come from `sfw-tun-broker.exe token-info <pid>`; build that binary
# (go build ./cmd/sfw-tun-broker) before running if you want the UI/engine/broker
# rows. With -BrokerPath, the installed broker is also used as the helper.
# The script never installs, starts or stops a service: installation belongs to
# scripts/install-tun-service.ps1, executed by the operator on an approved test
# machine.
param(
    [string]$BrokerPath = '',
    [string]$ResultPath = "$env:TEMP\singbox-service-security-result.json"
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'sfw-runtime.ps1')
$results = [ordered]@{ checks = @(); tokens = [ordered]@{}; manual = @() }
function Check([string]$name, [bool]$ok, [string]$detail = '') {
    $results.checks += [ordered]@{ name = $name; pass = $ok; detail = $detail }
    if (-not $ok) { $script:failed = $true }
    Write-Host ("{0} {1} {2}" -f ($(if ($ok) { 'PASS' } else { 'FAIL' })), $name, $detail)
}
function Find-TokenHelper {
    $repoRoot = Split-Path $PSScriptRoot -Parent
    $candidates = @(
        $BrokerPath,
        (Join-Path $repoRoot 'build\sfw-tun-broker.exe'),
        (Join-Path $repoRoot 'build\native\sfw-tun-broker.exe')
    )
    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate)) { return $candidate }
    }
    return $null
}
function Get-TokenFacts([int]$ProcessId) {
    $helper = Find-TokenHelper
    if (!$helper) { return $null }
    $raw = & $helper token-info $ProcessId 2>$null
    if ($LASTEXITCODE -ne 0 -or !$raw) { return $null }
    try { return ($raw | Out-String | ConvertFrom-Json) } catch { return $null }
}
function Get-SidOf($identity) {
    try { return $identity.Translate([Security.Principal.SecurityIdentifier]).Value } catch { return $identity.Value }
}
function Test-ContainsToken([string]$path) {
    if (!(Test-Path -LiteralPath $path)) { return $false }
    try { return (Get-Content -LiteralPath $path -Raw -ErrorAction Stop).Contains($runtime.Token) } catch { return $false }
}

$failed = $false
try {
    $runtime = Get-SfwRuntime
    $sid = $runtime.UserSid
    if (!$sid) { $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value }
    $systemSid = 'S-1-5-18'

    # --- 1. Engine token file and directory ACL (§0.2.2) -------------------
    $runDir = $runtime.Directory
    $tokenFile = Join-Path $runDir 'engine.token'
    $runtimeFile = Join-Path $runDir 'runtime.json'
    $dirAcl = Get-Acl -LiteralPath $runDir
    $fileAcl = Get-Acl -LiteralPath $tokenFile
    $dirExtra = @($dirAcl.Access | Where-Object { (Get-SidOf $_.IdentityReference) -notin @($sid, $systemSid) })
    Check 'token directory ACL is only user/SYSTEM' ($dirExtra.Count -eq 0) ("extra=" + (($dirExtra | ForEach-Object { $_.IdentityReference }) -join ','))
    Check 'token directory does not inherit parent ACEs' (-not $dirAcl.AreAccessRulesInherited) ''
    Check 'token directory is not a reparse point' (-not ((Get-Item -LiteralPath $runDir).Attributes -band [IO.FileAttributes]::ReparsePoint)) ''
    $fileExtra = @($fileAcl.Access | Where-Object { (Get-SidOf $_.IdentityReference) -notin @($sid, $systemSid) })
    Check 'engine.token ACL is only user/SYSTEM' ($fileExtra.Count -eq 0) ("extra=" + (($fileExtra | ForEach-Object { $_.IdentityReference }) -join ','))
    Check 'engine.token does not inherit parent ACEs' (-not $fileAcl.AreAccessRulesInherited) ''
    Check 'engine.token is not a reparse point' (-not ((Get-Item -LiteralPath $tokenFile).Attributes -band [IO.FileAttributes]::ReparsePoint)) ''
    Check 'runtime.json and engine.token are separate files' ((Test-Path -LiteralPath $runtimeFile) -and (Test-Path -LiteralPath $tokenFile) -and ($runtimeFile -ne $tokenFile)) ''
    $runtimeText = Get-Content -LiteralPath $runtimeFile -Raw
    Check 'runtime.json contains no token' (-not $runtimeText.Contains($runtime.Token)) ''
    Check 'runtime.json names this user and engine session' (($runtime.UserSid -eq ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value)) -and ($runtime.SessionId -eq (Get-Process -Id $runtime.Pid).SessionId)) "sid=$($runtime.UserSid) session=$($runtime.SessionId)"

    # --- 2. Program Files is not user-writable (§0.1) ----------------------
    $coreDir = 'C:\Program Files\SFW\Core'
    if (Test-Path -LiteralPath $coreDir) {
        $acl = Get-Acl -LiteralPath $coreDir
        $grants = @($acl.Access | Where-Object {
            $_.AccessControlType -eq 'Allow' -and
            ($_.FileSystemRights -band [Security.AccessControl.FileSystemRights]'Write,Modify,FullControl,CreateFiles,AppendData') -ne 0 -and
            (Get-SidOf $_.IdentityReference) -in @('S-1-5-11', 'S-1-1-0', 'S-1-5-32-545')
        })
        Check 'Program Files\SFW\Core not writable by users' ($grants.Count -eq 0) ("grants=" + (($grants | ForEach-Object { $_.IdentityReference }) -join ','))
    } else {
        Check 'Program Files\SFW\Core not writable by users' $false "missing: $coreDir (approved install package not present on this machine)"
    }

    # --- 3. HKCU proxy ownership (§0.3) -----------------------------------
    $proxy = Get-ItemProperty 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
    $state = Invoke-SfwAPI $runtime '/api/state'
    Check 'authenticated /api/state returns 200' ($null -ne $state) ''
    Check 'reported system_proxy matches this user HKCU' (([bool]$state.system_proxy) -eq ($proxy.ProxyEnable -eq 1)) ("ProxyEnable=$($proxy.ProxyEnable) state.system_proxy=$($state.system_proxy)")
    $systemHive = "Registry::HKEY_USERS\$systemSid\Software\Microsoft\Windows\CurrentVersion\Internet Settings"
    if (Test-Path $systemHive) {
        $systemProxy = Get-ItemProperty $systemHive -ErrorAction SilentlyContinue
        $polluted = ($systemProxy.ProxyEnable -eq 1) -and ($systemProxy.ProxyServer -eq $proxy.ProxyServer)
        Check 'SYSTEM hive was not set to the user proxy' (-not $polluted) "SYSTEM ProxyServer=$($systemProxy.ProxyServer)"
    } else {
        Check 'SYSTEM hive was not set to the user proxy' $true 'SYSTEM hive has no Internet Settings key'
    }

    # --- 4. Bearer gate on read AND write endpoints (§0.2.4) --------------
    # /api/quit is last: if the gate regressed it would stop the engine here and
    # the later checks would fail too, which is the intended loud failure.
    $writes = @(
        @{ Path = '/api/core/start'; Method = 'Post' },
        @{ Path = '/api/core/stop'; Method = 'Post' },
        @{ Path = '/api/proxy'; Method = 'Post'; Body = '{"enabled":false}' },
        @{ Path = '/api/node/select'; Method = 'Put'; Body = '{"group":"g","name":"n"}' },
        @{ Path = '/api/logs'; Method = 'Delete' },
        @{ Path = '/api/activate'; Method = 'Post'; Body = '{}' },
        @{ Path = '/api/quit'; Method = 'Post' }
    )
    $badWrites = @()
    foreach ($w in $writes) {
        $status = Invoke-SfwStatus -Uri ($runtime.Url + $w.Path) -Method $w.Method -Body $w.Body
        if ($status -ne 401) { $badWrites += "$($w.Method) $($w.Path)=$status" }
    }
    Check 'all write APIs reject a request without token (401)' ($badWrites.Count -eq 0) ($badWrites -join '; ')
    $badWrong = @()
    foreach ($w in $writes) {
        $status = Invoke-SfwStatus -Uri ($runtime.Url + $w.Path) -Method $w.Method -Headers @{ Authorization = 'Bearer definitely-wrong' } -Body $w.Body
        if ($status -ne 401) { $badWrong += "$($w.Method) $($w.Path)=$status" }
    }
    Check 'all write APIs reject a wrong token (401)' ($badWrong.Count -eq 0) ($badWrong -join '; ')
    Check 'read API rejects a wrong token (401)' ((Invoke-SfwStatus -Uri ($runtime.Url + '/api/state') -Headers @{ Authorization = 'Bearer definitely-wrong' }) -eq 401) ''

    # --- 5. Second-instance activation is authenticated (§0.2.5) ----------
    $profilesBefore = @(Invoke-SfwAPI $runtime '/api/profiles').Count
    $activateNoAuth = Invoke-SfwStatus -Uri ($runtime.Url + '/api/activate') -Method Post -Body '{}'
    Check 'activate without token is 401' ($activateNoAuth -eq 401) "status=$activateNoAuth"
    $activateWrong = Invoke-SfwStatus -Uri ($runtime.Url + '/api/activate') -Method Post -Headers @{ Authorization = 'Bearer wrong' } -Body '{}'
    Check 'activate with wrong token is 401' ($activateWrong -eq 401) "status=$activateWrong"
    $activateNoInstance = Invoke-SfwStatus -Uri ($runtime.Url + '/api/activate') -Method Post -Headers @{ Authorization = "Bearer $($runtime.Token)" } -Body '{}'
    Check 'activate without instance header is 401' ($activateNoInstance -eq 401) "status=$activateNoInstance"
    $activateRight = Invoke-SfwAPI $runtime '/api/activate' -Method Post -Body @{}
    Check 'empty activation with token succeeds (200) without opening a second engine' ($activateRight.activated -eq $false) ("activated=$($activateRight.activated)")
    $profilesAfter = @(Invoke-SfwAPI $runtime '/api/profiles').Count
    Check 'empty activation imports no profile' ($profilesAfter -eq $profilesBefore) "$profilesBefore -> $profilesAfter"
    $engines = @(Get-Process sing-box-win -ErrorAction SilentlyContinue)
    Check 'exactly one engine process is running' ($engines.Count -eq 1) "count=$($engines.Count)"

    # --- 6. Token facts: UI/engine are the user, broker is a service ------
    $helper = Find-TokenHelper
    if ($helper) {
        $engineFacts = Get-TokenFacts $runtime.Pid
        if ($engineFacts) {
            $results.tokens.engine = $engineFacts
            Check 'engine token is the interactive user' ($engineFacts.user_sid -eq $sid) "sid=$($engineFacts.user_sid)"
            Check 'engine token is not SYSTEM' (-not $engineFacts.is_local_system) "integrity=$($engineFacts.integrity_level)"
            Check 'engine token is not elevated' (-not $engineFacts.elevation) "type=$($engineFacts.elevation_type)"
        } else {
            Check 'engine token facts available' $false "token-info failed for pid $($runtime.Pid)"
        }
    } else {
        Check 'token-info helper available' $false 'build build\sfw-tun-broker.exe or pass -BrokerPath to the installed broker'
    }
    $ui = Get-Process SingBoxWin.Native -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($ui) {
        $uiFacts = Get-TokenFacts $ui.Id
        if ($uiFacts) {
            $results.tokens.ui = $uiFacts
            Check 'UI token is the interactive user' ($uiFacts.user_sid -eq $sid) "sid=$($uiFacts.user_sid)"
            Check 'UI token is not SYSTEM' (-not $uiFacts.is_local_system) "integrity=$($uiFacts.integrity_level)"
            Check 'UI token is not elevated' (-not $uiFacts.elevation) "type=$($uiFacts.elevation_type)"
            Check 'UI runs in the interactive session' ($uiFacts.session_id -eq $runtime.SessionId) "ui=$($uiFacts.session_id) engine=$($runtime.SessionId)"
        } else {
            Check 'UI token facts available' $false 'token-info failed; also check Get-Process SingBoxWin.Native | Select SessionId'
        }
        $session = (Get-Process -Id $ui.Id).SessionId
        Check 'UI session matches engine session' ($session -eq $runtime.SessionId) "ui=$session engine=$($runtime.SessionId)"
    } else {
        Check 'UI process present' $false 'SingBoxWin.Native is not running'
    }
    if ($BrokerPath) {
        $service = Get-CimInstance Win32_Service -Filter "Name='SFWTunBroker'" -ErrorAction SilentlyContinue
        if ($service -and $service.State -eq 'Running') {
            $brokerPid = [int]$service.ProcessId
            # A normal user usually cannot open a SYSTEM process token, so fall
            # back to the broker's own self-report over the ACL-restricted pipe
            # (hello). It comes from the broker token, never from the request.
            $brokerFacts = Get-TokenFacts $brokerPid
            if (!$brokerFacts) {
                try {
                    $hello = (& $helper hello 2>$null | Out-String | ConvertFrom-Json)
                    $brokerFacts = $hello.broker
                } catch { $brokerFacts = $null }
            }
            $results.tokens.broker = $brokerFacts
            Check 'broker token facts available' ($null -ne $brokerFacts) 'ProcessTokenInfo of a SYSTEM pid usually needs elevation; the pipe hello reports the broker token instead'
            Check 'broker token is a service identity, not the interactive user' ($brokerFacts -and ($brokerFacts.is_local_system -or $brokerFacts.user_sid -ne $sid)) ''
            Check 'broker token is not in the interactive session' ($brokerFacts -and ($brokerFacts.session_id -ne $runtime.SessionId)) "session=$($brokerFacts.session_id)"
            Check 'broker binary runs from the protected install directory' ($service.PathName -like '"C:\Program Files\SFW\Core\*') "path=$($service.PathName)"
            # hello claims the single TUN owner; ping must then succeed with the
            # issued capability, and recover must stay dry-run.
            $pingOk = $false
            try { $pingOk = ((& $helper ping 2>$null | Out-String | ConvertFrom-Json).ok -eq $true) } catch { $pingOk = $false }
            Check 'broker ping succeeds with a capability' $pingOk ''
            $recover = $null
            try { $recover = (& $helper recover 2>$null | Out-String | ConvertFrom-Json) } catch { $recover = $null }
            Check 'broker recover is dry-run only' ($recover -and $recover.dry_run -and (@($recover.planned | Where-Object { $_ -notmatch '^dry-run:' }).Count -eq 0)) ''
        } else {
            Check 'broker service is installed and running' $false 'sfw-tun-broker service not found or not running'
        }
    } else {
        $results.tokens.broker = 'not-installed: run with -BrokerPath after scripts/install-tun-service.ps1'
        Check 'broker token facts collected' $false 'broker not installed on this machine (expected until JARVIS installs the approved package)'
    }

    # --- 7. No token in logs or this report -------------------------------
    $dataDir = Join-Path $env:APPDATA 'sing-box-win'
    $logHits = @()
    foreach ($log in @(
            (Join-Path $dataDir 'engine.log'),
            (Join-Path $dataDir 'native-error.log'))) {
        if (Test-ContainsToken $log) { $logHits += (Split-Path $log -Leaf) }
    }
    Check 'token is absent from engine.log/native-error.log' ($logHits.Count -eq 0) ("hits=" + ($logHits -join ','))
    $reportPreview = $results | ConvertTo-Json -Depth 6
    Check 'token is absent from the result report' (-not $reportPreview.Contains($runtime.Token)) ''

    # --- 8. Second Windows user denial (manual) ---------------------------
    $results.manual += 'As a different Windows user on the same machine:'
    $results.manual += '  Get-Content <other user LOCALAPPDATA>\SFW\run\*\engine.token      # expected: OS access denied'
    $results.manual += '  Invoke-WebRequest <runtime.url>/api/state                        # expected: 401'
    $results.manual += '  Invoke-WebRequest <runtime.url>/api/activate -Method Post -Body "{}" # expected: 401'
    $results.manual += '  sc.exe stop SFWTunBroker                                         # expected: access denied (5)'
    $results.manual += 'Start SingBoxWin.Native.exe twice: only the first-run window activates; no second engine process and no profile import.'
    $results.manual += 'Search engine.log, native-error.log and this result file for the token value; expected: zero matches.'
} catch {
    $results.error = $_.Exception.Message
    $failed = $true
}
$results.pass = -not $failed
# The result file intentionally omits the token and all Authorization headers.
$results | ConvertTo-Json -Depth 6 | Set-Content $ResultPath -Encoding UTF8
if (!$results.pass) { exit 1 }
