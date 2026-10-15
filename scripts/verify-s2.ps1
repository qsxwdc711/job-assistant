#Requires -Version 7.0
param(
    [ValidateRange(1024,65535)][int]$HttpPort=18080,
    [string]$NodeExecutable,
    [string]$PnpmCli,
    [string]$GoProxy='https://proxy.golang.org,direct',
    [switch]$UseSystemProxy
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'web-tools.ps1')
$root=Split-Path -Parent $PSScriptRoot
$project='job-assistant-s2-verify-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$directory=Join-Path $root ('.cache/verification/'+$project)
New-Item -ItemType Directory -Path $directory -Force|Out-Null
function New-TestSecret { return [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
$password=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=')
$settings=[ordered]@{VERIFY_DB_PASSWORD=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=');VERIFY_ADMIN_PASSWORD=$password;VERIFY_CSRF_SECRET=(New-TestSecret);VERIFY_HTTP_PORT=$HttpPort;VERIFY_GOPROXY=$GoProxy}
$envFile=Join-Path $directory 'compose.env'
[string[]]$lines=@(foreach($key in $settings.Keys){$key+'='+$settings[$key]})
Set-Content -LiteralPath $envFile -Value $lines -Encoding utf8NoBOM
$compose=@('compose','--project-name',$project,'--env-file',$envFile,'-f',(Join-Path $root 'deploy/compose.verify.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s2.yaml'))
$previous=@{}
foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY','S2_BROWSER_PASSWORD','S2_BROWSER_BASE_URL')){$previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
function Invoke-TestCompose([string[]]$Commands,[string]$LogName){
    & docker @compose @Commands 2>&1|Tee-Object -FilePath (Join-Path $directory $LogName)
    if($LASTEXITCODE-ne0){throw ('Container verification failed: '+$LogName)}
}
try {
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('install','--frozen-lockfile')
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','build')
    if($UseSystemProxy){
        $target=[Uri]'https://registry-1.docker.io';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target)
        if($proxy.Host-eq$target.Host-or$proxy.Scheme-notin@('http','https')-or$proxy.UserInfo){throw 'A supported system HTTP proxy is required.'}
        $env:HTTP_PROXY=$proxy.AbsoluteUri;$env:HTTPS_PROXY=$proxy.AbsoluteUri;$env:NO_PROXY='localhost,127.0.0.1,::1'
    }
    Invoke-TestCompose @('--profile','tests','build','api','verifier') 'build.log'
    foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
    Invoke-TestCompose @('up','-d','--wait','--wait-timeout','90','db') 'db.log'
    Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','run','./tests/browser') 'fixture.log'
    Invoke-TestCompose @('up','-d','api') 'start.log'
    $baseURL='http://127.0.0.1:'+$HttpPort;$ready=$false
    for($attempt=0;$attempt-lt30;$attempt++){
        try{$health=Invoke-RestMethod -NoProxy -Uri ($baseURL+'/health/ready') -TimeoutSec 2;if($health.status-eq'ready'){$ready=$true;break}}catch{Start-Sleep -Milliseconds 500}
    }
    if(-not$ready){throw 'Container application did not become ready.'}
    $response=Invoke-WebRequest -NoProxy -Uri ($baseURL+'/jobs') -TimeoutSec 5
    if(-not$response.Content.Contains('<div id="app">')){throw 'Built web application was not served.'}
    $env:S2_BROWSER_PASSWORD=$password;$env:S2_BROWSER_BASE_URL=$baseURL
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','test:e2e')
    'PASS: desktop/mobile workflow, owner isolation, conflict recovery and hidden-job history.'|Set-Content -LiteralPath (Join-Path $directory 'browser.log')
    Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','vet','./...') 'vet.log'
    Invoke-TestCompose @('--profile','tests','run','--rm','verifier') 'race.log'
    if((Get-Content -LiteralPath (Join-Path $directory 'race.log') -Raw)-notmatch'ok\s+jobassistant/backend/tests/integration'){throw 'Real database integration tests did not execute.'}
    # Invalid input proves the production image contains the collector without making public requests.
    & docker @compose --profile tools run --rm collector invalid-source 2>&1|Tee-Object -FilePath (Join-Path $directory 'collector.log')
    if($LASTEXITCODE-ne1-or-not(Get-Content -LiteralPath (Join-Path $directory 'collector.log') -Raw).Contains('unknown public source')){throw 'Collector entrypoint validation failed.'}
    Invoke-TestCompose @('stop','-t','15','api') 'stop.log'
    $id=(& docker @compose ps -a -q api).Trim()
    $exitCode=(& docker inspect --format '{{.State.ExitCode}}' $id).Trim()
    if($LASTEXITCODE-ne0-or$exitCode-ne'0'){throw 'API did not stop cleanly.'}
    Invoke-TestCompose @('logs','--no-color','api') 'api.log'
    'PASS: S2 web, API, production collector entrypoint, go vet, Linux race with real PostgreSQL and graceful shutdown.'|Tee-Object -FilePath (Join-Path $directory 'result.log')
} finally {
    try {
        if($project-notmatch'^job-assistant-s2-verify-[a-f0-9]{8}$'){throw 'Unexpected cleanup project name.'}
        & docker @compose --profile tools --profile tests down --timeout 15
        if($LASTEXITCODE-ne0){throw ('Temporary resources require cleanup: '+$project)}
        'Temporary test containers and network removed; development data retained.'|Set-Content -LiteralPath (Join-Path $directory 'cleanup.log')
    } finally {foreach($key in $previous.Keys){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}}
}
