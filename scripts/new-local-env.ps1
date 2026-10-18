# Creates local development secrets without printing them or replacing an existing file.
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
$taskEnvPath = Join-Path $taskRoot '.env'
if (Test-Path -LiteralPath $taskEnvPath) {
    throw 'Local .env already exists; keep it or edit it explicitly.'
}

function New-LocalSecret {
    $bytes = New-Object byte[] 32
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
        return [Convert]::ToBase64String($bytes)
    } finally {
        $random.Dispose()
    }
}

$taskDatabasePassword = (New-LocalSecret).Replace('+', '-').Replace('/', '_').TrimEnd('=')
$taskAdminPassword = (New-LocalSecret).Replace('+', '-').Replace('/', '_').TrimEnd('=')
$taskSettings = [ordered]@{
    POSTGRES_USER = 'jobassistant'
    POSTGRES_DB = 'jobassistant'
    POSTGRES_PASSWORD = $taskDatabasePassword
    DATABASE_URL = 'postgres://jobassistant:' + $taskDatabasePassword + '@127.0.0.1:15432/jobassistant?sslmode=disable'
    CSRF_SECRET = New-LocalSecret
    NOTIFICATION_KEY = New-LocalSecret
    NOTIFICATIONS_SEND_ENABLED = 'false'
    AUTOMATION_ENABLED = 'true'
    ADMIN_USERNAME = 'admin'
    ADMIN_PASSWORD = $taskAdminPassword
    APP_ENV = 'development'
    APP_ORIGIN = 'http://localhost:8080'
    HTTP_ADDR = '127.0.0.1:8080'
}
[string[]]$taskLines = @(foreach ($taskName in $taskSettings.Keys) { $taskName + '=' + $taskSettings[$taskName] })
Set-Content -LiteralPath $taskEnvPath -Value $taskLines -Encoding utf8
Write-Output 'Local .env created. Secrets are stored in that ignored file; no application has been started.'
