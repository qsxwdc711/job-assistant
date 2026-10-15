#Requires -Version 7.0
param([string]$NodeExecutable, [string]$PnpmCli, [switch]$SkipBuild, [switch]$UseSystemProxy, [string]$GoProxy='https://proxy.golang.org,direct')
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'web-tools.ps1')
. (Join-Path $PSScriptRoot 'local-compose.ps1')
$root = Split-Path -Parent $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $root '.env'))) { & (Join-Path $PSScriptRoot 'new-local-env.ps1') }
$compose = Initialize-LocalCompose
function Invoke-LocalCompose([string[]]$Commands) { & docker @compose @Commands; if ($LASTEXITCODE -ne 0) { throw 'Local service command failed.' } }
if (-not $SkipBuild) {
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('install','--frozen-lockfile')
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','build')
    $previousProxy=@{}
    try {
        if($UseSystemProxy){
            $target=[Uri]'https://registry-1.docker.io';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target)
            if($proxy.Host-eq$target.Host-or$proxy.Scheme-notin@('http','https')-or$proxy.UserInfo){throw 'A supported system HTTP proxy is required.'}
            foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){$previousProxy[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
            $env:HTTP_PROXY=$proxy.AbsoluteUri;$env:HTTPS_PROXY=$proxy.AbsoluteUri;$env:NO_PROXY='localhost,127.0.0.1,::1'
        }
        Invoke-LocalCompose @('build','--build-arg',('GOPROXY='+$GoProxy),'api')
    } finally {foreach($key in $previousProxy.Keys){[Environment]::SetEnvironmentVariable($key,$previousProxy[$key],'Process')}}
}
if (-not (Test-Path -LiteralPath (Join-Path $root 'web/dist/index.html'))) { throw 'Build the web application before starting.' }
Invoke-LocalCompose @('up','-d','--wait','db')
Invoke-LocalCompose @('--profile','tools','run','--rm','admin','migrate')
# Existing accounts are preserved; bootstrap is used only on the first startup.
$count = & docker @compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT count(*) FROM users WHERE role=''admin''"'
if ($LASTEXITCODE -ne 0) { throw 'Administrator initialization check failed.' }
if ($count.Trim() -eq '0') { Invoke-LocalCompose @('--profile','tools','run','--rm','admin','bootstrap') }
Invoke-LocalCompose @('up','-d','api','worker')
Write-Output 'Local application started: http://127.0.0.1:8080 . Credentials remain in the ignored .env file.'
