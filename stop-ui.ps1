$ErrorActionPreference = 'Stop'
$dashboard = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'bin\dashboard.exe'))
$stopped = 0

foreach ($process in Get-CimInstance Win32_Process -Filter "Name='dashboard.exe'") {
    if ($process.ExecutablePath -and [IO.Path]::GetFullPath($process.ExecutablePath) -eq $dashboard) {
        Stop-Process -Id $process.ProcessId -Force
        $stopped++
    }
}

if ($stopped -eq 0) {
    Write-Host 'AppleServices dashboard is not running.'
} else {
    Write-Host "Stopped $stopped AppleServices dashboard process(es)."
}
