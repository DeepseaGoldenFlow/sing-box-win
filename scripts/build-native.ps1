$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    # Non-interactive sessions (e.g. launched from WSL) may not have the machine-wide
    # .NET SDK on PATH or set DOTNET_ROOT, so VS MSBuild's DotNetSdkResolver cannot
    # resolve Microsoft.NET.Sdk. Point it at the standard install location.
    $programFiles = if ($env:ProgramFiles) { $env:ProgramFiles } else { 'C:\Program Files' }
    $dotnetRoot = Join-Path $programFiles 'dotnet'
    if (Test-Path (Join-Path $dotnetRoot 'dotnet.exe')) {
        if (!$env:DOTNET_ROOT -or !(Test-Path (Join-Path $env:DOTNET_ROOT 'dotnet.exe'))) { $env:DOTNET_ROOT = $dotnetRoot }
        if (($env:PATH -split ';' | ForEach-Object { $_.TrimEnd('\') }) -notcontains $dotnetRoot) { $env:PATH = "$dotnetRoot;$env:PATH" }
        Write-Host "[env] DOTNET_ROOT=$env:DOTNET_ROOT"
    } else {
        Write-Warning "dotnet.exe not found under $dotnetRoot; MSBuild may fail to resolve Microsoft.NET.Sdk."
    }
    Write-Host '[1/4] Building native client (Release, self-contained win-x64)...'
    # WinUI PRI packaging tasks require Visual Studio MSBuild on this machine.
    $vswhere = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
    $msbuild = if (Test-Path $vswhere) { & $vswhere -latest -products '*' -find 'MSBuild\**\Bin\amd64\MSBuild.exe' | Select-Object -First 1 }
    if (!$msbuild) { throw 'Visual Studio MSBuild with Windows App SDK build tools is required.' }
    & $msbuild native\SingBoxWin.Native\SingBoxWin.Native.csproj /restore /p:Configuration=Release /p:RuntimeIdentifier=win-x64 /p:Platform=x64 /p:SelfContained=true /p:WindowsAppSDKSelfContained=true /v:minimal /nologo
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
    # On Windows `python` may only be the Microsoft Store execution alias: it resolves on
    # PATH but fails at run time (exit 9009), so probe for real interpreters instead.
    $python = $null
    foreach ($candidate in @('python', 'python3')) {
        if (!(Get-Command $candidate -ErrorAction SilentlyContinue)) { continue }
        try { $probe = & $candidate -c 'print(1)' 2>$null } catch { continue }
        if ($LASTEXITCODE -eq 0 -and "$probe".Trim()) { $python = $candidate; break }
    }
    if ($python) {
        & $python scripts\make-release-zip.py
        if ($LASTEXITCODE -ne 0) { throw "make-release-zip.py failed: $LASTEXITCODE" }
    } else {
        Write-Warning 'No working python found; build\native.zip not created. Run scripts/make-release-zip.py on the Linux side.'
    }
    $count = (Get-ChildItem 'build\native' -File -Recurse | Measure-Object).Count
    Write-Host "Done: build\native.zip ($count files in build\native)"
} catch {
    Write-Error $_ -ErrorAction Continue
    exit 1
} finally { Pop-Location }
