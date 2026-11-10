param(
    [ValidateRange(1024, 65535)][int]$Port = 18741,
    [switch]$NoOpen
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$stateDir = 'C:\ProgramData\AppleServices\state'
$dashboard = Join-Path $root 'bin\dashboard.exe'
$worker = Join-Path $root 'bin\trusted-device-gui.exe'
$library = Join-Path $root '.cache\openbubbles-store\rust_lib_bluebubbles.dll'
$pushSetup = Join-Path $root 'tools\mac-push-check.py'
$python = (Get-Command python.exe -ErrorAction Stop).Source
$gccDir = 'C:\msys64\ucrt64\bin'
$url = "http://127.0.0.1:$Port"

foreach ($path in @($dashboard, $worker, $library, $pushSetup, $python, (Join-Path $gccDir 'gcc.exe'))) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required local component is missing: $path"
    }
}

New-Item -ItemType Directory -Force -Path $stateDir | Out-Null

try {
    $existing = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
} catch {
    $existing = $null
}
if ($existing) {
    if ($existing.Headers['X-AppleServices-UI'] -ne '1') {
        throw "Port $Port is already used by another service."
    }
    if (-not $NoOpen) {
        Start-Process $url
    }
    Write-Host "AppleServices dashboard is already running: $url"
    return
}

function Quote-NativeArgument([string]$value) {
    return '"' + (($value -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}

$arguments = @(
    '-dir', $stateDir,
    '-worker', $worker,
    '-adi-dll', $library,
    '-python', $python,
    '-mac-push-setup', $pushSetup,
    '-port', [string]$Port
)
$quotedArguments = @($arguments | ForEach-Object { Quote-NativeArgument $_ }) -join ' '
$oldPath = $env:Path
try {
    $env:Path = "$gccDir;$oldPath"
    $process = Start-Process -FilePath $dashboard -ArgumentList $quotedArguments `
        -WorkingDirectory $root -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $stateDir 'dashboard.out.log') `
        -RedirectStandardError (Join-Path $stateDir 'dashboard.err.log')
} finally {
    $env:Path = $oldPath
}

$deadline = [DateTime]::UtcNow.AddSeconds(20)
do {
    if ($process.HasExited) {
        throw 'Dashboard stopped during startup. Check C:\ProgramData\AppleServices\state\dashboard.err.log.'
    }
    Start-Sleep -Milliseconds 250
    try {
        $ready = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
    } catch {
        $ready = $null
    }
} until (($ready -and $ready.Headers['X-AppleServices-UI'] -eq '1') -or [DateTime]::UtcNow -ge $deadline)

if (-not $ready) {
    throw "Dashboard startup is still pending. PID: $($process.Id)"
}
if (-not $NoOpen) {
    Start-Process $url
}
Write-Host "AppleServices dashboard ready: $url (PID $($process.Id))"
