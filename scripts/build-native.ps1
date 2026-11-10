$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    Write-Host '[1/4] Building native client (Release, self-contained win-x64)...'
    dotnet build native\SingBoxWin.Native\SingBoxWin.Native.csproj -c Release -r win-x64 -p:Platform=x64 -p:SelfContained=true -p:WindowsAppSDKSelfContained=true
    if ($LASTEXITCODE -ne 0) { throw "Native build failed: $LASTEXITCODE" }
    $output = 'native\SingBoxWin.Native\bin\x64\Release\net8.0-windows10.0.19041.0\win-x64'
    if (!(Test-Path "$output\SingBoxWin.Native.exe")) { throw "Native output missing: $output" }
    if (!(Test-Path 'build\sing-box-win.exe')) { throw 'Engine missing: build\sing-box-win.exe. Build the Go engine first.' }
    Write-Host '[2/4] Replacing build\native with complete build output...'
    if (Test-Path 'build\native') { Remove-Item 'build\native' -Recurse -Force }
    New-Item 'build\native' -ItemType Directory -Force | Out-Null
    Copy-Item "$output\*" 'build\native' -Recurse -Force
    Write-Host '[3/4] Copying Go engine...'
    Copy-Item 'build\sing-box-win.exe' 'build\native\sing-box-win.exe' -Force
    Write-Host '[4/4] Creating build\native.zip...'
    if (Test-Path 'build\native.zip') { Remove-Item 'build\native.zip' -Force }
    if (Get-Command python -ErrorAction SilentlyContinue) {
        python scripts\make-release-zip.py
        if ($LASTEXITCODE -ne 0) { throw "make-release-zip.py failed: $LASTEXITCODE" }
    } else {
        Write-Warning 'No python; run scripts/make-release-zip.py on the Linux side'
    }
    $count = (Get-ChildItem 'build\native' -File -Recurse | Measure-Object).Count
    Write-Host "Done: build\native.zip ($count files in build\native)"
} catch {
    Write-Error $_ -ErrorAction Continue
    exit 1
} finally { Pop-Location }
