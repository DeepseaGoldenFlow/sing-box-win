# API integration test using only an isolated, inactive temporary profile.
# Every call is authenticated with the engine token from the ACL-protected run
# directory (docs/proposal-tun-windows.md §0.2.4); there is no bare loopback API.
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'sfw-runtime.ps1')
$sfw=Get-SfwRuntime
function Api([string]$path,[string]$method='GET',$body=$null) {
    $p=@{Uri=$sfw.Url+$path;Method=$method;Headers=(Get-SfwHeaders $sfw)}
    if($null -ne $body){$p.ContentType='application/json';$p.Body=($body|ConvertTo-Json -Compress)}
    Invoke-RestMethod @p
}
$original=Api '/api/state'
$id=$null; $job=$null
$result=[ordered]@{}
try {
    $job=Start-Job {
        $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
        $listener.Start()
        $listener.LocalEndpoint.Port
        for($request=0;$request -lt 2;$request++) {
        $client=$listener.AcceptTcpClient()
        try {
            $stream=$client.GetStream()
            $reader=[IO.StreamReader]::new($stream)
            do { $line=$reader.ReadLine() } while ($line -ne '')
            $body='{"outbounds":[{"type":"direct","tag":"windows-profile-test"}]}'
            $bytes=[Text.Encoding]::UTF8.GetBytes($body)
            $header=[Text.Encoding]::ASCII.GetBytes("HTTP/1.1 200 OK`r`nContent-Type: application/json`r`nContent-Length: "+$bytes.Length+"`r`nConnection: close`r`n`r`n")
            $stream.Write($header,0,$header.Length);$stream.Write($bytes,0,$bytes.Length)
        } finally { $client.Close() }
        }
        $listener.Stop()
    }
    for($i=0;$i -lt 50;$i++) {
        $port=Receive-Job $job -Keep|Select-Object -First 1
        if($port) {break}
        Start-Sleep -Milliseconds 100
    }
    if(!$port) {throw 'Mock subscription server did not start'}
    $p=Api '/api/profiles/import' 'Post' @{name='__profile_management_test__';url="http://127.0.0.1:$port/config.json"}
    $id=$p.id
    $path='/api/profiles/'+$id
    Api $path 'Put' @{name='__profile_management_edited__';url="http://127.0.0.1:$port/config.json";auto_update=$true;update_minutes=5} | Out-Null
    $detail=Api $path
    if($detail.profile.name -ne '__profile_management_edited__' -or $detail.profile.update_minutes -ne 5 -or !$detail.profile.auto_update) {throw 'Profile metadata edit failed'}
    $result.edit_name_url_and_auto_update=$true
    Api ($path+'/update') 'Post' | Out-Null
    $detail=Api $path
    if($detail.content -notmatch 'windows-profile-test') {throw 'Subscription update did not save response'}
    $result.subscription_update_and_readback=$true
    $rejected=$false
    try { Api $path 'Put' @{content='invalid JSON'} | Out-Null } catch { $rejected=$true }
    if(!$rejected -or (Api $path).content -ne $detail.content) {throw 'Invalid JSON overwrote configuration'}
    $result.invalid_content_preserves_original=$true
    Api $path 'Delete' | Out-Null
    if((Api '/api/profiles').id -contains $id) {throw 'Deleted profile still listed'}
    if(!(Get-ChildItem "$env:APPDATA\sing-box-win\profile-trash" -Filter "$id-*.json")) {throw 'Deletion backup missing'}
    $result.delete_and_recovery_copy=$true
    $now=Api '/api/state'
    if($now.profile -ne $original.profile -or !$now.running -or !$now.system_proxy) {throw 'Test disturbed the active profile'}
    $result.active_profile_unchanged=$true
    $result.ok=$true
} catch { $result.ok=$false;$result.error=$_.Exception.Message }
finally {
    if($job) { Stop-Job $job -ErrorAction SilentlyContinue; Remove-Job $job -Force -ErrorAction SilentlyContinue }
    if($id) {
        try { if((Api '/api/profiles').id -contains $id) {Api ('/api/profiles/'+$id) 'Delete'|Out-Null} }catch{}
        foreach($dir in @('profile-history','profile-trash')) { Get-ChildItem "$env:APPDATA\sing-box-win\$dir" -Filter "$id-*.json" -ErrorAction SilentlyContinue | Remove-Item -Force }
    }
}
$result|ConvertTo-Json
if(!$result.ok){exit 1}
