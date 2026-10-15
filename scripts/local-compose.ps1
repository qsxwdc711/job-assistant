# Prepare an ignored container configuration from the existing local secrets.
function Initialize-LocalCompose {
    $root = Split-Path -Parent $PSScriptRoot
    $settings = [ordered]@{}
    foreach ($line in Get-Content -LiteralPath (Join-Path $root '.env')) {
        if ($line -match '^([A-Z0-9_]+)=(.*)$') { $settings[$Matches[1]] = $Matches[2] }
    }
    foreach ($key in @('POSTGRES_USER','POSTGRES_DB','POSTGRES_PASSWORD','DATABASE_URL','CSRF_SECRET','ADMIN_USERNAME','ADMIN_PASSWORD')) {
        if (-not $settings[$key]) { throw ('Missing local setting: ' + $key) }
    }
    $uri = [Uri]$settings.DATABASE_URL
    if ($uri.Host -notin @('127.0.0.1','localhost','db')) { throw 'Local startup requires a local project database.' }
    $builder = [UriBuilder]$uri; $builder.Host = 'db'; $builder.Port = 5432
    $settings.DATABASE_URL = $builder.Uri.AbsoluteUri
    $settings.APP_ENV = 'development'; $settings.APP_ORIGIN = 'http://127.0.0.1:8080'
    $directory = Join-Path $root '.cache/local'
    New-Item -ItemType Directory -Path $directory -Force | Out-Null
    $filename = Join-Path $directory 'compose.env'
    [string[]]$lines = @(foreach ($key in $settings.Keys) { $key + '=' + $settings[$key] })
    Set-Content -LiteralPath $filename -Value $lines -Encoding utf8NoBOM
    return @('compose','--env-file',$filename,'-f',(Join-Path $root 'deploy/compose.yaml'),'-f',(Join-Path $root 'deploy/compose.local.yaml'),'-f',(Join-Path $root 'deploy/compose.web.yaml'))
}
