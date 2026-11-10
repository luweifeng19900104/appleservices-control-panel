param(
    [Parameter(Mandatory = $true)][string]$Address,
    [Parameter(Mandatory = $true)][string]$PublicAddress,
    [ValidateRange(1024, 65535)][int]$Port = 18742,
    [switch]$NoOpen
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$gateway = Join-Path $root 'bin\remote-gateway.exe'
$remoteDir = 'C:\ProgramData\AppleServices\remote'
$cert = Join-Path $remoteDir 'tls-cert.pem'
$key = Join-Path $remoteDir 'tls-key.pem'
$stateDir = 'C:\ProgramData\AppleServices\state'
$url = 'https://{0}:{1}' -f $PublicAddress, $Port

foreach ($path in @($gateway, $cert, $key)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required remote-access component is missing: $path"
    }
}
if (-not (Get-NetIPAddress -AddressFamily IPv4 -IPAddress $Address -ErrorAction SilentlyContinue)) {
    throw "This server does not currently own IPv4 address $Address."
}

& (Join-Path $root 'run-ui.ps1') -NoOpen

$fullGateway = [IO.Path]::GetFullPath($gateway)
$existing = Get-CimInstance Win32_Process -Filter "Name='remote-gateway.exe'" |
    Where-Object { $_.ExecutablePath -and [IO.Path]::GetFullPath($_.ExecutablePath) -eq $fullGateway } |
    Select-Object -First 1
if ($existing) {
    if (-not $NoOpen) {
        Start-Process $url
    }
    Write-Host "AppleServices remote gateway is already running: $url"
    return
}

function Quote-NativeArgument([string]$value) {
    return '"' + (($value -replace '(\\*)"', '$1$1\"') -replace '(\\+)$', '$1$1') + '"'
}

$listen = '{0}:{1}' -f $Address, $Port
$arguments = @(
    '-listen', $listen,
    '-target', 'http://127.0.0.1:18741',
    '-tls-cert', $cert,
    '-tls-key', $key
)
$quotedArguments = @($arguments | ForEach-Object { Quote-NativeArgument $_ }) -join ' '
$process = Start-Process -FilePath $gateway -ArgumentList $quotedArguments `
    -WorkingDirectory $root -WindowStyle Hidden -PassThru `
    -RedirectStandardOutput (Join-Path $stateDir 'remote-gateway.out.log') `
    -RedirectStandardError (Join-Path $stateDir 'remote-gateway.err.log')

$deadline = [DateTime]::UtcNow.AddSeconds(15)
do {
    if ($process.HasExited) {
        throw 'Remote gateway stopped during startup. Check remote-gateway.err.log.'
    }
    Start-Sleep -Milliseconds 250
    $ready = Test-NetConnection -ComputerName $Address -Port $Port -InformationLevel Quiet -WarningAction SilentlyContinue
} until ($ready -or [DateTime]::UtcNow -ge $deadline)

if (-not $ready) {
    throw "Remote gateway startup is still pending. PID: $($process.Id)"
}
if (-not $NoOpen) {
    Start-Process $url
}
Write-Host "AppleServices remote gateway ready: $url (PID $($process.Id))"
