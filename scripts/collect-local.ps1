#Requires -Version 7.0
param([ValidateSet('all','tencent-campus','huawei-campus','cmb-campus')][string]$Source='all', [switch]$UseSystemProxy)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'local-compose.ps1')
$compose=Initialize-LocalCompose
$previous=@{}
try {
    if($UseSystemProxy){
        $target=[Uri]'https://join.qq.com';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target)
        if($proxy.Host-eq$target.Host-or$proxy.Scheme-notin@('http','https')-or$proxy.UserInfo){throw 'A supported system HTTP proxy is required.'}
        $builder=[UriBuilder]$proxy;$builder.Host='host.docker.internal'
        foreach($key in @('COLLECTOR_HTTP_PROXY','COLLECTOR_HTTPS_PROXY')){$previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process');[Environment]::SetEnvironmentVariable($key,$builder.Uri.AbsoluteUri,'Process')}
    }
    & docker @compose --profile tools run --rm collector $Source
    if($LASTEXITCODE-ne0){throw 'Some sources failed. Previous jobs were retained; inspect the source status in the management page.'}
} finally {
    foreach($key in $previous.Keys){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
}
