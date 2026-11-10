# Build the Windows distribution into dist\windows (run on Windows, from any directory).
#   powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1
# The result has the same layout as the original app\ folder: the source tree
# (needed at runtime by tools\mac-push-check.py, nativeadi\*.py, gsa\*.pem),
# bin\*.exe and the Windows launch scripts at the top level. It contains no
# state, no .cache (pinned DLL) and no Python/compiler runtime.
param([string]$Go = 'go')
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $root 'dist\windows'
if (Test-Path -LiteralPath $out) { Remove-Item -LiteralPath $out -Recurse -Force }
New-Item -ItemType Directory -Force -Path (Join-Path $out 'bin') | Out-Null
Push-Location $root
try {
    $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
    & $Go build -o (Join-Path $out 'bin\login.exe') ./cmd/login; if ($LASTEXITCODE -ne 0) { throw 'login build failed' }
    & $Go build -o (Join-Path $out 'bin\dashboard.exe') ./cmd/dashboard; if ($LASTEXITCODE -ne 0) { throw 'dashboard build failed' }
    & $Go build -o (Join-Path $out 'bin\trusted-device-gui.exe') ./cmd/trusted-device; if ($LASTEXITCODE -ne 0) { throw 'trusted-device build failed' }
    Copy-Item -LiteralPath (Join-Path $out 'bin\trusted-device-gui.exe') -Destination (Join-Path $out 'bin\trusted-device.exe')
    $files = & git ls-files
    if ($LASTEXITCODE -ne 0) { throw 'git ls-files failed' }
    foreach ($file in $files) {
        if ($file -like 'platform/*' -or $file -like 'scripts/*' -or $file -eq 'Makefile') { continue }
        $target = Join-Path $out ($file -replace '/', '\')
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $target) | Out-Null
        Copy-Item -LiteralPath (Join-Path $root $file) -Destination $target
    }
    Copy-Item -Path (Join-Path $root 'platform\windows\*') -Destination $out
    Get-FileHash -Algorithm SHA256 (Join-Path $out 'bin\*.exe') | ForEach-Object { '{0}  bin/{1}' -f $_.Hash.ToLower(), (Split-Path -Leaf $_.Path) } | Set-Content -LiteralPath (Join-Path $out 'SHA256SUMS') -Encoding ascii
    $zip = Join-Path $root 'dist\appleservices-windows-dist.zip'
    if (Test-Path -LiteralPath $zip) { Remove-Item -LiteralPath $zip }
    Compress-Archive -Path (Join-Path $out '*') -DestinationPath $zip
    Write-Output "built $out"
} finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}
