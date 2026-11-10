$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
    & 'C:\Users\giraf\tools\dotnet\dotnet.exe' publish native\SingBoxWin.Native\SingBoxWin.Native.csproj -c Release -r win-x64 --self-contained true -p:WindowsAppSDKSelfContained=true -p:Platform=x64 -o build\native
    if ($LASTEXITCODE -ne 0) { throw "Native publish failed: $LASTEXITCODE" }
} finally { Pop-Location }
