$ErrorActionPreference = 'Stop'
$authFile = 'C:ProgramDataAppleServicesstatedashboard-auth.json'

& (Join-Path $PSScriptRoot 'stop-ui.ps1')
if (Test-Path -LiteralPath $authFile) {
    [IO.File]::Delete($authFile)
}
& (Join-Path $PSScriptRoot 'run-ui.ps1') -NoOpen

Write-Host 'Dashboard password removed. The next browser visit will create a new password.'
