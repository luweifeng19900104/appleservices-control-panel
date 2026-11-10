param([switch]$NoOpen, [switch]$Stop, [string]$AppDir)
$ErrorActionPreference = 'Stop'
# Original layout: this script sits next to app\ (D:\appleservices-family-private\run-ui.ps1).
# Distribution layout (dist\windows): the script sits inside the app folder itself.
if (-not $AppDir) {
    if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'app\bin')) { $AppDir = Join-Path $PSScriptRoot 'app' } else { $AppDir = $PSScriptRoot }
}
$app = [IO.Path]::GetFullPath($AppDir)
# The Python venv and the w64devkit compiler live next to app\ in the original
# layout; look there first, then in the app folder itself.
$runtimeRoot = $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $runtimeRoot '.venv\Scripts\python.exe'))) {
    $parent = Split-Path -Parent $app
    if (Test-Path -LiteralPath (Join-Path $parent '.venv\Scripts\python.exe')) { $runtimeRoot = $parent }
}
$exe = Join-Path $app 'bin\dashboard.exe'
$url = 'http://127.0.0.1:18741/'
$listener = Get-NetTCPConnection -LocalPort 18741 -State Listen -ErrorAction SilentlyContinue
if ($listener) {
    $owner = Get-Process -Id $listener.OwningProcess
    if ($owner.Path -ne $exe) { throw 'Port 18741 belongs to another program.' }
    if ($Stop) {
        Stop-Process -Id $owner.Id
        Write-Output 'Dashboard stopped; saved account state retained.'
        return
    }
} elseif ($Stop) {
    Write-Output 'Dashboard already stopped.'
    return
} else {
    foreach ($binary in @('dashboard.exe','trusted-device-gui.exe','login.exe')) {
        if (-not (Test-Path -LiteralPath (Join-Path $app "bin\$binary"))) {
            throw "Missing bin\$binary; see LOCAL-SETUP.md for build commands."
        }
    }
    $state = Join-Path $app 'state'
    New-Item -ItemType Directory -Force -Path $state | Out-Null
    $python = Join-Path $runtimeRoot '.venv\Scripts\python.exe'
    $compilerDir = Join-Path $runtimeRoot 'tools\w64devkit\bin'
    $library = Join-Path $app '.cache\openbubbles-store\rust_lib_bluebubbles.dll'
    if (-not (Test-Path -LiteralPath $python) -or -not (Test-Path -LiteralPath (Join-Path $compilerDir 'gcc.exe'))) {
        throw 'Local Python/compiler runtime is missing; see LOCAL-SETUP.md.'
    }
    if (-not (Test-Path -LiteralPath $library) -or (Get-FileHash -LiteralPath $library -Algorithm SHA256).Hash -ne 'ca88e816dda449d7ba24d54a5ed709a2d392daeae28c25a61dcd2e7496e44cfe') {
        throw 'Pinned native module is missing or invalid; run setup-installed-native.ps1.'
    }
    & $python -c 'import cryptography, requests'
    if ($LASTEXITCODE -ne 0) { throw 'Local Python dependencies are missing; see LOCAL-SETUP.md.' }
    $arguments = @('-dir', ('"' + $state + '"'), '-worker', ('"' + (Join-Path $app 'bin\trusted-device-gui.exe') + '"'), '-python', ('"' + $python + '"'), '-port', '18741')
    # The panel must start without inheriting any ambient login credentials.
    $previousID = $env:APPLESERVICES_APPLEID
    $previousPassword = $env:APPLESERVICES_PASSWORD
    $previousPath = $env:PATH
    try {
        $env:PATH = $compilerDir + ';' + $env:PATH
        $env:APPLESERVICES_APPLEID = $null
        $env:APPLESERVICES_PASSWORD = $null
        $process = Start-Process -FilePath $exe -ArgumentList $arguments -WorkingDirectory $app -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $state 'dashboard.out.log') -RedirectStandardError (Join-Path $state 'dashboard.err.log')
    } finally {
        $env:APPLESERVICES_APPLEID = $previousID
        $env:APPLESERVICES_PASSWORD = $previousPassword
        $env:PATH = $previousPath
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        if ($process.HasExited) { throw 'Dashboard exited; inspect app\state\dashboard.err.log.' }
        try { $response = Invoke-WebRequest $url -UseBasicParsing -TimeoutSec 1 } catch { $response = $null }
        if ($response -and $response.Headers['X-AppleServices-UI'] -eq '1') { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $response -or $response.Headers['X-AppleServices-UI'] -ne '1') { throw 'Dashboard did not become ready.' }
}
if (-not $NoOpen) { Start-Process $url }
