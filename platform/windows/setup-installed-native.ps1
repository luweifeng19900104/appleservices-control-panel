$ErrorActionPreference = 'Stop'
$package = Get-AppxPackage -Name OpenBubbles.OpenBubbles
if (-not $package -or $package.Version -ne '1.18.800.0' -or $package.Architecture -ne 'X64' -or
    $package.Publisher -ne 'CN=A7258527-3356-42FA-AABD-F6BAA9420BDE' -or
    $package.SignatureKind -ne 'Store' -or $package.Status -ne 'Ok' -or $package.IsDevelopmentMode) {
    throw 'Requires the exact verified OpenBubbles 1.18.800.0 x64 Store installation.'
}
$candidates = @(Get-ChildItem -LiteralPath $package.InstallLocation -Filter rust_lib_bluebubbles.dll -Recurse)
if ($candidates.Count -ne 1) { throw 'Native library is missing or ambiguous.' }
$expected = 'ca88e816dda449d7ba24d54a5ed709a2d392daeae28c25a61dcd2e7496e44cfe'
if ((Get-FileHash -LiteralPath $candidates[0].FullName -Algorithm SHA256).Hash -ne $expected) {
    throw 'Native library hash mismatch; no module copied.'
}
# Original layout: next to app\. Distribution layout: inside the app folder.
$appRoot = if (Test-Path -LiteralPath (Join-Path $PSScriptRoot 'app\bin')) { Join-Path $PSScriptRoot 'app' } else { $PSScriptRoot }
$directory = Join-Path $appRoot '.cache\openbubbles-store'
New-Item -ItemType Directory -Path $directory -Force | Out-Null
Copy-Item -LiteralPath $candidates[0].FullName -Destination (Join-Path $directory 'rust_lib_bluebubbles.dll') -Force
Write-Output 'READY: Store identity verified; pinned native DLL installed'
