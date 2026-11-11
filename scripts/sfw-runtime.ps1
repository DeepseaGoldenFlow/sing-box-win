# Shared runtime discovery for the Windows test scripts.
#
# The engine publishes runtime.json and engine.token in an ACL-protected run
# directory (%LOCALAPPDATA%\SFW\run\<instance-id>) instead of the old
# %APPDATA%\sing-box-win\runtime.json. Every Engine API call now needs
# "Authorization: Bearer <token>" (docs/proposal-tun-windows.md §0.2).
#
# Never print $runtime.Token or include it in a result file: the token is a
# secret. Only assert on request outcomes.

function Get-SfwRuntime {
    [CmdletBinding()]
    param([string]$Root = (Join-Path $env:LOCALAPPDATA 'SFW\run'))

    if (!(Test-Path -LiteralPath $Root)) { throw "SFW run directory missing: $Root" }
    foreach ($dir in Get-ChildItem -LiteralPath $Root -Directory | Sort-Object LastWriteTimeUtc -Descending) {
        # A redirected run directory, record or token must never be trusted.
        if ($dir.Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        $runtimePath = Join-Path $dir.FullName 'runtime.json'
        $tokenPath = Join-Path $dir.FullName 'engine.token'
        if (!(Test-Path -LiteralPath $runtimePath) -or !(Test-Path -LiteralPath $tokenPath)) { continue }
        if ((Get-Item -LiteralPath $runtimePath).Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        if ((Get-Item -LiteralPath $tokenPath).Attributes -band [IO.FileAttributes]::ReparsePoint) { continue }
        try { $r = Get-Content -LiteralPath $runtimePath -Raw | ConvertFrom-Json } catch { continue }
        if ($r.protocol_version -ne 1) { continue }
        if (-not $r.url -or -not $r.instance_id -or -not $r.pid) { continue }
        $process = Get-Process -Id $r.pid -ErrorAction SilentlyContinue
        if (!$process) { continue }
        if ($r.process_start_time) {
            $actual = $process.StartTime.ToUniversalTime().ToString('O')
            if ($actual -ne $r.process_start_time) { continue }
        }
        $token = (Get-Content -LiteralPath $tokenPath -Raw).Trim()
        if ($token -notmatch '^[A-Za-z0-9_-]{32,128}$') { throw 'Engine token file is malformed; refusing to send it' }
        if ($r.url -notmatch '^http://(127\.0\.0\.1|\[::1\]):\d{1,5}$') { throw "Engine runtime URL is not a plain loopback origin: $($r.url)" }
        return [pscustomobject]@{
            Url        = $r.url
            Token      = $token
            InstanceId = $r.instance_id
            Pid        = $r.pid
            UserSid    = $r.user_sid
            SessionId  = $r.session_id
            Directory  = $dir.FullName
            Runtime    = $r
        }
    }
    throw "No live authenticated SFW engine runtime under $Root"
}

function Get-SfwHeaders {
    param([Parameter(Mandatory = $true)]$Runtime)
    return @{ Authorization = "Bearer $($Runtime.Token)"; 'X-SFW-Instance' = $Runtime.InstanceId }
}

# Invoke-SfwStatus returns the HTTP status code without throwing on 4xx/5xx.
# This works on Windows PowerShell 5.1 (no -SkipHttpErrorCheck) and PowerShell 7,
# so the 401 assertions below do not depend on the operator's shell edition.
function Invoke-SfwStatus {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [string]$Method = 'GET',
        [hashtable]$Headers = @{},
        [string]$Body = $null,
        [string]$ContentType = 'application/json'
    )
    $parameters = @{ Uri = $Uri; Method = $Method; Headers = $Headers; UseBasicParsing = $true }
    # [string]$Body coerces $null to '' so '-ne $null' is always true; check emptiness.
    if (-not [string]::IsNullOrEmpty($Body)) { $parameters.Body = $Body; $parameters.ContentType = $ContentType }
    elseif ($Method -ne 'GET') { $parameters.Body = '{}'; $parameters.ContentType = $ContentType }
    try {
        $response = Invoke-WebRequest @parameters -ErrorAction Stop
        return [int]$response.StatusCode
    } catch {
        $ex = $_.Exception
        if ($ex.Response) { return [int]$ex.Response.StatusCode }
        if ($ex.InnerException -and $ex.InnerException.Response) { return [int]$ex.InnerException.Response.StatusCode }
        throw
    }
}

# Invoke-SfwAPI centralises the Authorization header so no test can fall back to
# an unauthenticated request by accident.
function Invoke-SfwAPI {
    param(
        [Parameter(Mandatory = $true)]$Runtime,
        [Parameter(Mandatory = $true)][string]$Path,
        [string]$Method = 'GET',
        $Body = $null
    )
    $parameters = @{
        Uri             = $Runtime.Url + $Path
        Method          = $Method
        Headers         = (Get-SfwHeaders $Runtime)
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = ($Body | ConvertTo-Json -Compress)
    }
    elseif ($Method -ne 'GET') { $parameters.ContentType = 'application/json'; $parameters.Body = '{}' }
    return Invoke-RestMethod @parameters
}
