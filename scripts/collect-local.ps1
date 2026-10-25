#Requires -Version 7.0
param([ValidatePattern('^[a-z0-9][a-z0-9-]{0,80}$')][string]$Source='all', [switch]$UseSystemProxy)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'local-compose.ps1')
$compose=Initialize-LocalCompose
$root=Split-Path -Parent $PSScriptRoot
$settings=@{}
foreach($line in Get-Content -LiteralPath (Join-Path $root '.env')){if($line-match'^([A-Z0-9_]+)=(.*)$'){$settings[$Matches[1]]=$Matches[2]}}
$hostSources=@($settings.COLLECTOR_HOST_SOURCES-split','|ForEach-Object{$_.Trim()}|Where-Object{$_})
$previous=@{}
try {
    if($UseSystemProxy){
        $target=[Uri]'https://join.qq.com';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target)
        if($proxy.Host-eq$target.Host-or$proxy.Scheme-notin@('http','https')-or$proxy.UserInfo){throw 'A supported system HTTP proxy is required.'}
        $builder=[UriBuilder]$proxy;$builder.Host='host.docker.internal'
        foreach($key in @('COLLECTOR_HTTP_PROXY','COLLECTOR_HTTPS_PROXY')){$previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process');[Environment]::SetEnvironmentVariable($key,$builder.Uri.AbsoluteUri,'Process')}
    }
    # Use the same selected runtimes as scheduled updates. With no host sources,
    # retain the existing single-container invocation, including its all mode.
    $selected=@($Source)
    if($Source-eq'all'-and$hostSources.Count){
		$sourceList = & docker @compose --profile tools run --rm collector list
		if($LASTEXITCODE-ne0){throw 'Unable to list enabled collection sources.'}
		$selected=@(($sourceList -join "`n") | ConvertFrom-Json)
    }
    $failed=$false
    foreach($code in $selected){
        if($code-in$hostSources){
            $database=[Uri]$settings.DATABASE_URL
            if($database.Host-notin@('localhost','127.0.0.1')){throw 'Host collection requires the local project database.'}
            $hostEnvironment=@{}
            try{
                foreach($key in @('DATABASE_URL','HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){$hostEnvironment[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
                $env:DATABASE_URL=$settings.DATABASE_URL;$env:HTTP_PROXY='';$env:HTTPS_PROXY='';$env:NO_PROXY='*'
                & go -C (Join-Path $root 'backend') run ./cmd/collector $code
                if($LASTEXITCODE-ne0){$failed=$true}
            }finally{foreach($key in $hostEnvironment.Keys){[Environment]::SetEnvironmentVariable($key,$hostEnvironment[$key],'Process')}}
        }else{
            & docker @compose --profile tools run --rm collector $code
            if($LASTEXITCODE-ne0){$failed=$true}
        }
    }
    if($failed){throw 'Some sources failed. Previous jobs were retained; inspect the source status in the management page.'}
} finally {
    foreach($key in $previous.Keys){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
}
