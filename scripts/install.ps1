# Builds hb with its full version and installs it into Go's bin folder
# (GOBIN, or GOPATH\bin), which should be on PATH.
#   version: <VERSION>+<UTC build date>.<commit count>.g<short sha>
#   e.g.     0.1.0+20261115.12.g1a2b3c4
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $base = (Get-Content -Raw VERSION).Trim()
    if ($base -notmatch '^\d+\.\d+\.\d+$') { throw "VERSION must be <major>.<minor>.<patch>, got '$base'" }
    $count = (git rev-list --count HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'no commits yet: commit first, the version names one' }
    $sha = (git rev-parse --short HEAD).Trim()
    $date = [DateTime]::UtcNow.ToString('yyyyMMdd')
    $version = "$base+$date.$count.g$sha"
    if (git status --porcelain) {
        Write-Warning "uncommitted changes: $version names g$sha, but the build includes them"
    }
    go install -trimpath -ldflags "-s -w -X hb/internal/cmd.Version=$version" ./cmd/hb
    if ($LASTEXITCODE -ne 0) { throw 'go install failed' }
    $bin = (go env GOBIN)
    if (-not $bin) { $bin = Join-Path (go env GOPATH) 'bin' }
    Write-Host "installed $version -> $(Join-Path $bin 'hb.exe')"
} finally {
    Pop-Location
}
