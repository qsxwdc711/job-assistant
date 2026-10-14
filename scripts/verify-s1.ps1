#Requires -Version 7.0

param(
    [ValidateRange(1024, 65535)]
    [int]$HttpPort = 18080,
    [string]$GoProxy = 'https://proxy.golang.org,direct',
    [switch]$UseSystemProxy,
    [switch]$NoCache
)

$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskProject = 'job-assistant-verify-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
$taskDirectory = Join-Path $taskRoot ('.cache/verification/' + $taskProject)
$taskEnvPath = Join-Path $taskDirectory 'compose.env'
$taskComposePath = Join-Path $taskRoot 'deploy/compose.verify.yaml'
New-Item -ItemType Directory -Path $taskDirectory -Force | Out-Null

function New-VerificationSecret {
    $bytes = New-Object byte[] 32
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    } finally {
        $random.Dispose()
    }
}

$taskSettings = [ordered]@{
    VERIFY_DB_PASSWORD = (New-VerificationSecret).Replace('+', '-').Replace('/', '_').TrimEnd('=')
    VERIFY_ADMIN_PASSWORD = (New-VerificationSecret).Replace('+', '-').Replace('/', '_').TrimEnd('=')
    VERIFY_CSRF_SECRET = New-VerificationSecret
    VERIFY_HTTP_PORT = $HttpPort
    VERIFY_GOPROXY = $GoProxy
}
[string[]]$taskEnvLines = @(foreach ($taskName in $taskSettings.Keys) { $taskName + '=' + $taskSettings[$taskName] })
Set-Content -LiteralPath $taskEnvPath -Value $taskEnvLines -Encoding utf8
$taskComposeArguments = @('compose', '--project-name', $taskProject, '--env-file', $taskEnvPath, '-f', $taskComposePath)
$taskPreviousProxy = @{}

function Restore-VerificationProxy {
    foreach ($taskName in $taskPreviousProxy.Keys) {
        if ($null -eq $taskPreviousProxy[$taskName]) {
            Remove-Item -LiteralPath ('Env:\' + $taskName) -ErrorAction SilentlyContinue
        } else {
            [Environment]::SetEnvironmentVariable($taskName, $taskPreviousProxy[$taskName], 'Process')
        }
    }
}

function Invoke-VerificationCompose {
    param([string[]]$Commands, [string]$LogName)
    if ($LogName) {
        & docker @taskComposeArguments @Commands 2>&1 | Tee-Object -FilePath (Join-Path $taskDirectory $LogName)
    } else {
        & docker @taskComposeArguments @Commands
    }
    if ($LASTEXITCODE -ne 0) {
        throw ('Docker verification command failed with exit code ' + $LASTEXITCODE + '.')
    }
}

function Get-VerificationHTTPStatus {
    param([string]$Uri, [Microsoft.PowerShell.Commands.WebRequestSession]$Session)
    try {
        $response = Invoke-WebRequest -Uri $Uri -WebSession $Session -NoProxy -TimeoutSec 10
        return [int]$response.StatusCode
    } catch {
        if ($null -eq $_.Exception.Response) { throw }
        return [int]$_.Exception.Response.StatusCode
    }
}

try {
    if ($UseSystemProxy) {
        $taskRegistry = [Uri]'https://registry-1.docker.io'
        $taskProxy = [Net.WebRequest]::GetSystemWebProxy().GetProxy($taskRegistry)
        if ($taskProxy -eq $taskRegistry -or $taskProxy.Scheme -notin @('http', 'https')) {
            throw 'No supported system HTTP proxy was configured for the registry.'
        }
        foreach ($taskName in @('HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY')) {
            $taskPreviousProxy[$taskName] = [Environment]::GetEnvironmentVariable($taskName, 'Process')
        }
        [Environment]::SetEnvironmentVariable('HTTP_PROXY', $taskProxy.AbsoluteUri, 'Process')
        [Environment]::SetEnvironmentVariable('HTTPS_PROXY', $taskProxy.AbsoluteUri, 'Process')
        [Environment]::SetEnvironmentVariable('NO_PROXY', 'localhost,127.0.0.1,::1', 'Process')
    }
    Invoke-VerificationCompose -Commands @('config', '--quiet')
    $taskBuildArguments = @('--profile', 'tests', 'build')
    if ($NoCache) { $taskBuildArguments += '--no-cache' }
    $taskBuildArguments += @('api', 'verifier')
    Invoke-VerificationCompose -Commands $taskBuildArguments -LogName 'build.log'
    # Registry proxy variables belong only to the build client, not local HTTP verification.
    Restore-VerificationProxy
    Invoke-VerificationCompose -Commands @('up', '-d', '--wait', '--wait-timeout', '90', 'db')
    Invoke-VerificationCompose -Commands @('--profile', 'tools', 'run', '--rm', 'admin', 'migrate') -LogName 'migration.log'
    Invoke-VerificationCompose -Commands @('--profile', 'tools', 'run', '--rm', 'admin', 'bootstrap') -LogName 'bootstrap.log'
    & docker @taskComposeArguments --profile tools run --rm admin bootstrap 2>&1 | Tee-Object -FilePath (Join-Path $taskDirectory 'bootstrap-repeat.log')
    if ($LASTEXITCODE -ne 1 -or -not ((Get-Content (Join-Path $taskDirectory 'bootstrap-repeat.log') -Raw).Contains('administrator already exists'))) {
        throw 'Repeated administrator bootstrap was not correctly rejected.'
    }
    Invoke-VerificationCompose -Commands @('up', '-d', 'api')

    $taskBaseURL = 'http://127.0.0.1:' + $HttpPort
    $taskDeadline = [DateTimeOffset]::UtcNow.AddSeconds(30)
    $taskReady = $false
    $taskReadinessError = 'unexpected health response'
    while ([DateTimeOffset]::UtcNow -lt $taskDeadline) {
        try {
            $taskHealth = Invoke-RestMethod -Uri ($taskBaseURL + '/health/ready') -NoProxy -TimeoutSec 2
            if ($taskHealth.status -eq 'ready') { $taskReady = $true; break }
        } catch {
            $taskReadinessError = $_.Exception.Message
            Start-Sleep -Milliseconds 500
        }
    }
    if (-not $taskReady) { throw ('Container API did not become ready: ' + $taskReadinessError) }
    $taskAnonymous = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    if ((Get-VerificationHTTPStatus -Uri ($taskBaseURL + '/api/v1/auth/me') -Session $taskAnonymous) -ne 401) {
        throw 'Anonymous identity request was accepted.'
    }
    $taskLoginBody = @{username = 'admin'; password = $taskSettings.VERIFY_ADMIN_PASSWORD} | ConvertTo-Json -Compress
    $taskLogin = Invoke-RestMethod -Method Post -Uri ($taskBaseURL + '/api/v1/auth/login') -ContentType 'application/json' -Body $taskLoginBody -SessionVariable taskBrowser -NoProxy -TimeoutSec 10
    $taskMe = Invoke-RestMethod -Uri ($taskBaseURL + '/api/v1/auth/me') -WebSession $taskBrowser -NoProxy -TimeoutSec 10
    $taskUsers = Invoke-RestMethod -Uri ($taskBaseURL + '/api/v1/admin/users') -WebSession $taskBrowser -NoProxy -TimeoutSec 10
    if ($taskMe.effective_role -ne 'admin' -or $taskUsers.items.Count -ne 1) { throw 'Container administrator identity was incorrect.' }
    $taskLogout = Invoke-WebRequest -Method Post -Uri ($taskBaseURL + '/api/v1/auth/logout') -WebSession $taskBrowser -Headers @{'X-CSRF-Token' = $taskLogin.csrf_token} -NoProxy -TimeoutSec 10
    if ($taskLogout.StatusCode -ne 204 -or (Get-VerificationHTTPStatus -Uri ($taskBaseURL + '/api/v1/auth/me') -Session $taskBrowser) -ne 401) {
        throw 'Container logout did not invalidate the session.'
    }
    'PASS: container migration, bootstrap, readiness, authentication, authorization and CSRF logout.' | Tee-Object -FilePath (Join-Path $taskDirectory 'smoke.log')

    Invoke-VerificationCompose -Commands @('--profile', 'tests', 'run', '--rm', 'verifier') -LogName 'race.log'
    if ((Get-Content (Join-Path $taskDirectory 'race.log') -Raw) -notmatch 'ok\s+jobassistant/backend/tests/integration') {
        throw 'Real database integration tests did not execute successfully.'
    }
    Invoke-VerificationCompose -Commands @('stop', '-t', '15', 'api')
    $taskAPIID = (& docker @taskComposeArguments ps -a -q api).Trim()
    if ($LASTEXITCODE -ne 0 -or -not $taskAPIID) { throw 'Stopped API container could not be inspected.' }
    $taskExitCode = (& docker inspect --format '{{.State.ExitCode}}' $taskAPIID).Trim()
    if ($LASTEXITCODE -ne 0 -or $taskExitCode -ne '0') { throw 'Container API did not exit cleanly.' }
    Invoke-VerificationCompose -Commands @('logs', '--no-color', 'api') -LogName 'api.log'
    if (-not ((Get-Content (Join-Path $taskDirectory 'api.log') -Raw).Contains('API stopped cleanly'))) {
        throw 'Graceful shutdown completion was not recorded.'
    }
    'PASS: Linux race tests with real PostgreSQL and graceful container shutdown.' | Tee-Object -FilePath (Join-Path $taskDirectory 'result.log')
} catch {
    try {
        & docker @taskComposeArguments logs --no-color api 2>&1 | Tee-Object -FilePath (Join-Path $taskDirectory 'api-failure.log')
    } catch {
        Write-Warning 'API diagnostics were unavailable; the original verification failure is retained.'
    }
    throw
} finally {
    try {
        # This random project name is owned by this invocation. No persistent volume is used.
        if ($taskProject -notmatch '^job-assistant-verify-[a-f0-9]{8}$') { throw 'Unexpected cleanup project name.' }
        & docker @taskComposeArguments --profile tools --profile tests down --timeout 15
        if ($LASTEXITCODE -ne 0) { throw ('Verification cleanup failed for ' + $taskProject + '.') }
        'PASS: temporary verification containers and network removed.' | Set-Content -LiteralPath (Join-Path $taskDirectory 'cleanup.log') -Encoding utf8
    } finally {
        Restore-VerificationProxy
    }
}
