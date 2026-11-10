$ErrorActionPreference = 'Stop'
$gateway = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'bin\remote-gateway.exe'))
$stopped = 0

foreach ($process in Get-CimInstance Win32_Process -Filter "Name='remote-gateway.exe'") {
    if ($process.ExecutablePath -and [IO.Path]::GetFullPath($process.ExecutablePath) -eq $gateway) {
        Stop-Process -Id $process.ProcessId -Force
        $stopped++
    }
}

if ($stopped -eq 0) {
    Write-Host 'AppleServices remote gateway is not running.'
} else {
    Write-Host "Stopped $stopped AppleServices remote gateway process(es)."
}
