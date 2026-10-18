# Shared by local startup and isolated verification. No global toolchain changes.
function Invoke-ProjectWeb {
    param([string[]]$Commands, [string]$NodeExecutable, [string]$PnpmCli, [string]$ProjectRoot)
    $root = Split-Path -Parent $PSScriptRoot
    if ($ProjectRoot) { $root = (Resolve-Path -LiteralPath $ProjectRoot -ErrorAction Stop).Path }
    if (-not $NodeExecutable) { $NodeExecutable = (Get-Command node -ErrorAction Stop).Source }
    $version = (& $NodeExecutable -p 'process.versions.node').Trim()
    if ($LASTEXITCODE -ne 0 -or [version]$version -lt [version]'22.18.0' -or [version]$version -ge [version]'23.0.0') {
        throw 'Use the project Node 22 toolchain (22.18.0 or newer). Pass -NodeExecutable when PATH points to an older Node.'
    }
    $previousPath = $env:PATH
    try {
        $env:PATH = (Split-Path -Parent $NodeExecutable) + [IO.Path]::PathSeparator + $env:PATH
        if ($PnpmCli) { & $NodeExecutable $PnpmCli --dir (Join-Path $root 'web') @Commands }
        else { & pnpm --dir (Join-Path $root 'web') @Commands }
        if ($LASTEXITCODE -ne 0) { throw 'The frontend command failed.' }
    } finally { $env:PATH = $previousPath }
}
