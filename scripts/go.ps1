$env:GOTELEMETRY = 'off'
$env:GOPATH = Join-Path $PSScriptRoot '../.cache/go'
$env:GOCACHE = Join-Path $PSScriptRoot '../.cache/build'
& go @args
exit $LASTEXITCODE
