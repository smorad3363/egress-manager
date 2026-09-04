$ErrorActionPreference = "Stop"

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$go = Get-Command go -ErrorAction SilentlyContinue
$docker = Get-Command docker -ErrorAction SilentlyContinue
$node = Get-Command node -ErrorAction SilentlyContinue
if (-not $node) {
    $nodeDirectory = "C:\Program Files\nodejs"
    if (Test-Path -LiteralPath (Join-Path $nodeDirectory "node.exe")) {
        $env:Path = $nodeDirectory + ";" + $env:Path
    } else {
        throw "Node.js not found"
    }
}

$pnpm = Get-Command pnpm -ErrorAction SilentlyContinue
if (-not $pnpm) {
    $localPnpm = Get-ChildItem "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\pnpm.pnpm_*\pnpm.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $localPnpm) {
        throw "pnpm not found"
    }
    $pnpm = $localPnpm
}

if ($go) {
    & $go.Source test ./...
    & $go.Source vet ./...
} elseif ($docker) {
    & $docker.Source run --rm --volume "${repositoryRoot}:/src" --workdir /src golang:1.27.1 go test ./...
    & $docker.Source run --rm --volume "${repositoryRoot}:/src" --workdir /src golang:1.27.1 go vet ./...
} else {
    throw "Go toolchain not found and Docker fallback is unavailable"
}

& $pnpm.FullName --dir web typecheck
& $pnpm.FullName --dir web lint
& $pnpm.FullName --dir web build
