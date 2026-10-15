#Requires -Version 7.0
param(
    [ValidateRange(1024,65535)][int]$HttpPort = 18081,
    [string]$NodeExecutable,
    [string]$PnpmCli,
    [string]$GoProxy = 'https://proxy.golang.org,direct',
    [switch]$UseSystemProxy
)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'web-tools.ps1')
$root = Split-Path -Parent $PSScriptRoot
$project = 'job-assistant-full-verify-' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$directory = Join-Path $root ('.cache/verification/' + $project)
New-Item -ItemType Directory -Path $directory -Force | Out-Null
function New-TestSecret { return [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
$password = (New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=')
$settings = [ordered]@{
    VERIFY_DB_PASSWORD = (New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=')
    VERIFY_ADMIN_PASSWORD = $password
    VERIFY_CSRF_SECRET = New-TestSecret
    VERIFY_HTTP_PORT = $HttpPort
    VERIFY_GOPROXY = $GoProxy
}
$envFile = Join-Path $directory 'compose.env'
Set-Content -LiteralPath $envFile -Encoding utf8NoBOM -Value @(
    foreach ($key in $settings.Keys) { $key + '=' + $settings[$key] }
)
$compose = @('compose','--project-name',$project,'--env-file',$envFile,'-f',(Join-Path $root 'deploy/compose.verify.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s2.yaml'))
$previous = @{}
foreach ($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY','S2_BROWSER_PASSWORD','S2_BROWSER_BASE_URL','S2_BROWSER_AUTH_CACHE')) {
    $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process')
}
function Invoke-TestCompose([string[]]$Commands, [string]$LogName) {
    & docker @compose @Commands 2>&1 | Tee-Object -FilePath (Join-Path $directory $LogName)
    if ($LASTEXITCODE -ne 0) { throw ('Container verification failed: ' + $LogName) }
}
try {
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('install','--frozen-lockfile')
    Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','build')
    if ($UseSystemProxy) {
        $target = [Uri]'https://registry-1.docker.io'
        $proxy = [Net.WebRequest]::GetSystemWebProxy().GetProxy($target)
        if ($proxy.Host -eq $target.Host -or $proxy.Scheme -notin @('http','https') -or $proxy.UserInfo) { throw 'A supported system HTTP proxy is required.' }
        $env:HTTP_PROXY = $proxy.AbsoluteUri; $env:HTTPS_PROXY = $proxy.AbsoluteUri; $env:NO_PROXY = 'localhost,127.0.0.1,::1'
    }
    Invoke-TestCompose @('--profile','tests','build','api','verifier') 'build.log'
    foreach ($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY')) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    Invoke-TestCompose @('up','-d','--wait','--wait-timeout','90','db') 'db.log'

    # Synthetic copies are labelled QA and exist only in this tmpfs _test database.
    # The historical official snapshot supplies valid shape, never a real application.
    $original = Get-Content -LiteralPath (Join-Path $root 'backend/tests/browser/testdata/tencent-job.json') -Raw
    $documents = @(foreach ($index in 1..106) {
        $document = ($original | ConvertFrom-Json -AsHashtable).items[0]
        $document.external_id = 'functional-' + $index
        $document.campaign_key = 'isolated-functional-tests'
        $document.campaign_name = '隔离功能测试数据'
        $document.campaign_evidence = 'Synthetic QA fixture; not a real job advertisement.'
        $document.title = 'QA-' + $index.ToString('000')
        $document.public_status = @('open','unknown','closed')[($index - 1) % 3]
        $document.recruitment_type = @('campus','internship','early_career')[($index - 1) % 3]
        $document.written_test = @('yes','no','unknown')[($index - 1) % 3]
        $document.deadline_date = '2099-01-' + ((($index - 1) % 28) + 1).ToString('00')
        $document.deadline_at = $null
        $backendVariant = $document.variants[0]
        $beijing = @($backendVariant.locations | Where-Object { $_.city -eq '北京市' })
        $backendVariant.locations = @($backendVariant.locations | Where-Object { $_.city -eq '深圳市' })
        $backendVariant.departments = @()
        $backendVariant.name = '后端方向'
        $frontendVariant = ($backendVariant | ConvertTo-Json -Depth 50 | ConvertFrom-Json -AsHashtable)
        $frontendVariant.key = 'frontend'; $frontendVariant.name = '前端方向'; $frontendVariant.direction = 'frontend'; $frontendVariant.locations = $beijing
        $document.variants = @($backendVariant,$frontendVariant)
        $document
    })
    $fixturePath = Join-Path $directory 'jobs.json'
    @{ items = $documents } | ConvertTo-Json -Depth 50 | Set-Content -LiteralPath $fixturePath -Encoding utf8NoBOM
    Invoke-TestCompose @('--profile','tests','run','--rm','--volume',($fixturePath + ':/tmp/functional-jobs.json:ro'),'--env','S2_BROWSER_JOB_FILE=/tmp/functional-jobs.json','--entrypoint','go','verifier','run','./tests/browser') 'fixture.log'
    Invoke-TestCompose @('up','-d','api') 'start.log'
    $baseURL = 'http://127.0.0.1:' + $HttpPort
    $ready = $false
    for ($attempt=0; $attempt -lt 30; $attempt++) {
        try { $health = Invoke-RestMethod -NoProxy -Uri ($baseURL + '/health/ready') -TimeoutSec 2; if ($health.status -eq 'ready') { $ready=$true; break } } catch { Start-Sleep -Milliseconds 500 }
    }
    if (-not $ready) { throw 'Container application did not become ready.' }
    $env:S2_BROWSER_PASSWORD = $password; $env:S2_BROWSER_BASE_URL = $baseURL
    $env:S2_BROWSER_AUTH_CACHE = Join-Path $directory 'browser-auth.json'
    $browserFailure = $null
    try {
        Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('exec','playwright','test','--config','playwright.functional.config.ts')
    } catch { $browserFailure = $_ }
    # Preserve the machine-readable report before cleanup, including failed assertions.
    $report = Join-Path $root '.cache/verification/full-browser-results.json'
    if (Test-Path -LiteralPath $report) { Copy-Item -LiteralPath $report -Destination (Join-Path $directory 'browser-results.json') }
    Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','gofmt','verifier','-l','.') 'gofmt.log'
    if (Select-String -LiteralPath (Join-Path $directory 'gofmt.log') -Pattern '\.go$' -Quiet) { throw 'Go formatting differences were found.' }
    Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','vet','./...') 'vet.log'
    Invoke-TestCompose @('--profile','tests','run','--rm','verifier') 'race.log'
    if ((Get-Content -LiteralPath (Join-Path $directory 'race.log') -Raw) -notmatch 'ok\s+jobassistant/backend/tests/integration') { throw 'Real database integration tests did not execute.' }
    Invoke-TestCompose @('stop','-t','15','api') 'stop.log'
    $id = (& docker @compose ps -a -q api).Trim()
    if ((& docker inspect --format '{{.State.ExitCode}}' $id).Trim() -ne '0') { throw 'API did not stop cleanly.' }
    Invoke-TestCompose @('logs','--no-color','api') 'api.log'
    if ($browserFailure) {
        'FAIL: functional browser assertions failed; see browser-results.json. Go race and vet passed.' | Set-Content -LiteralPath (Join-Path $directory 'result.log')
        throw $browserFailure
    }
    'PASS: full functional checks, Go race and vet.' | Tee-Object -FilePath (Join-Path $directory 'result.log')
} finally {
    try {
        if ($project -notmatch '^job-assistant-full-verify-[a-f0-9]{8}$') { throw 'Unexpected cleanup project name.' }
        & docker @compose --profile tools --profile tests down --timeout 15
        if ($LASTEXITCODE -ne 0) { throw ('Temporary resources require cleanup: ' + $project) }
        'Temporary test containers and network removed; development data retained.' | Set-Content -LiteralPath (Join-Path $directory 'cleanup.log')
        Write-Host ('Evidence directory: ' + $directory)
    } finally {
        foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    }
}
