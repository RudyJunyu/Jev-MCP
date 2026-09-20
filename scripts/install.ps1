param(
  [ValidateSet('codex','claude','all')][string]$Client = 'all',
  [string]$Config = (Join-Path $PSScriptRoot '../jev-client.json')
)
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$binary = Join-Path $root 'bin/jev-mcphub.exe'
if (-not (Test-Path -LiteralPath $binary)) {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.26.4+, or place the release binary in bin/jev-mcphub.exe.' }
  Push-Location $root
  try { & (Join-Path $PSScriptRoot 'go.ps1') build -o $binary ./cmd/jev-mcphub; if ($LASTEXITCODE -ne 0) { throw 'Build failed' } }
  finally { Pop-Location }
}
& $binary install --client $Client --config $Config
exit $LASTEXITCODE
