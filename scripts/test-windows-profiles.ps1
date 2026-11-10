# API integration test using only an isolated, inactive temporary profile.
$ErrorActionPreference='Stop'
$r=Get-Content "$env:APPDATA\sing-box-win\runtime.json" -Raw|ConvertFrom-Json
$original=Invoke-RestMethod ($r.url+'/api/state')
$id=$null; $job=$null
$result=[ordered]@{}
try {
    $job=Start-Job {
        $listener=[Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback,0)
        $listener.Start()
        $listener.LocalEndpoint.Port
        $client=$listener.AcceptTcpClient()
        try {
            $stream=$client.GetStream()
            $reader=[IO.StreamReader]::new($stream)
            do { $line=$reader.ReadLine() } while ($line -ne '')
            $body='{"outbounds":[{"type":"direct","tag":"windows-profile-test"}]}'
            $bytes=[Text.Encoding]::UTF8.GetBytes($body)
            $header=[Text.Encoding]::ASCII.GetBytes("HTTP/1.1 200 OK`r`nContent-Type: application/json`r`nContent-Length: "+$bytes.Length+"`r`nConnection: close`r`n`r`n")
            $stream.Write($header,0,$header.Length);$stream.Write($bytes,0,$bytes.Length)
        } finally { $client.Close();$listener.Stop() }
    }
    for($i=0;$i -lt 50;$i++) {
        $port=Receive-Job $job -Keep|Select-Object -First 1
        if($port) {break}
        Start-Sleep -Milliseconds 100
    }
    if(!$port) {throw 'Mock subscription server did not start'}
    $p=Invoke-RestMethod ($r.url+'/api/profiles/import') -Method Post -ContentType application/json -Body '{"name":"__profile_management_test__","content":"{}"}'
    $id=$p.id
    $path=$r.url+'/api/profiles/'+$id
    Invoke-RestMethod $path -Method Put -ContentType application/json -Body (@{name='__profile_management_edited__';url="http://127.0.0.1:$port/config.json";auto_update=$true;update_minutes=5}|ConvertTo-Json) | Out-Null
    $detail=Invoke-RestMethod $path
    if($detail.profile.name -ne '__profile_management_edited__' -or $detail.profile.update_minutes -ne 5 -or !$detail.profile.auto_update) {throw 'Profile metadata edit failed'}
    $result.edit_name_url_and_auto_update=$true
    Invoke-RestMethod ($path+'/update') -Method Post | Out-Null
    $detail=Invoke-RestMethod $path
    if($detail.content -notmatch 'windows-profile-test') {throw 'Subscription update did not save response'}
    $result.subscription_update_and_readback=$true
    $rejected=$false
    try { Invoke-RestMethod $path -Method Put -ContentType application/json -Body '{"content":"invalid JSON"}' | Out-Null } catch { $rejected=$true }
    if(!$rejected -or (Invoke-RestMethod $path).content -ne $detail.content) {throw 'Invalid JSON overwrote configuration'}
    $result.invalid_content_preserves_original=$true
    Invoke-RestMethod $path -Method Delete | Out-Null
    if((Invoke-RestMethod ($r.url+'/api/profiles')).id -contains $id) {throw 'Deleted profile still listed'}
    if(!(Get-ChildItem "$env:APPDATA\sing-box-win\profile-trash" -Filter "$id-*.json")) {throw 'Deletion backup missing'}
    $result.delete_and_recovery_copy=$true
    $now=Invoke-RestMethod ($r.url+'/api/state')
    if($now.profile -ne $original.profile -or !$now.running -or !$now.system_proxy) {throw 'Test disturbed the active profile'}
    $result.active_profile_unchanged=$true
    $result.ok=$true
} catch { $result.ok=$false;$result.error=$_.Exception.Message }
finally {
    if($job) { Stop-Job $job -ErrorAction SilentlyContinue; Remove-Job $job -Force -ErrorAction SilentlyContinue }
    if($id) {
        try { if((Invoke-RestMethod ($r.url+'/api/profiles')).id -contains $id) {Invoke-RestMethod ($r.url+'/api/profiles/'+$id) -Method Delete|Out-Null} }catch{}
        foreach($dir in @('profile-history','profile-trash')) { Get-ChildItem "$env:APPDATA\sing-box-win\$dir" -Filter "$id-*.json" -ErrorAction SilentlyContinue | Remove-Item -Force }
    }
}
$result|ConvertTo-Json
if(!$result.ok){exit 1}
