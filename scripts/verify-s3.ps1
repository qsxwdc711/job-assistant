#Requires -Version 7.0
param(
 [ValidateRange(1024,65535)][int]$HttpPort=18083,
 [string]$NodeExecutable,[string]$PnpmCli,[string]$GoProxy='https://proxy.golang.org,direct',
 [switch]$UseSystemProxy
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'web-tools.ps1')
$root=Split-Path -Parent $PSScriptRoot
$project='job-assistant-s3-verify-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$directory=Join-Path $root ('.cache/verification/'+$project)
New-Item -ItemType Directory -Path $directory -Force|Out-Null
function New-TestSecret { [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
$password=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=')
$settings=[ordered]@{VERIFY_DB_PASSWORD=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=');VERIFY_ADMIN_PASSWORD=$password;VERIFY_CSRF_SECRET=(New-TestSecret);VERIFY_HTTP_PORT=$HttpPort;VERIFY_GOPROXY=$GoProxy}
$envFile=Join-Path $directory 'compose.env'
Set-Content -LiteralPath $envFile -Value @(foreach($key in $settings.Keys){$key+'='+$settings[$key]}) -Encoding utf8NoBOM
$compose=@('compose','--project-name',$project,'--env-file',$envFile,'-f',(Join-Path $root 'deploy/compose.verify.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s2.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s3.yaml'))
$previous=@{}
foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY','S2_BROWSER_PASSWORD','S2_BROWSER_BASE_URL','S3_BROWSER_FIXTURE')){$previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
function Invoke-TestCompose([string[]]$Commands,[string]$LogName){
 & docker @compose @Commands 2>&1|Tee-Object -FilePath (Join-Path $directory $LogName)
 if($LASTEXITCODE-ne0){throw ('S3 verification failed: '+$LogName)}
}
try{
 Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('install','--frozen-lockfile')
 Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','build')
 if($UseSystemProxy){$target=[Uri]'https://registry-1.docker.io';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target);if($proxy.Host-eq$target.Host-or$proxy.UserInfo){throw 'System HTTP proxy unavailable.'};$env:HTTP_PROXY=$proxy.AbsoluteUri;$env:HTTPS_PROXY=$proxy.AbsoluteUri;$env:NO_PROXY='localhost,127.0.0.1,::1'}
 Invoke-TestCompose @('--profile','tests','build','api','verifier') 'build.log'
 foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
 Invoke-TestCompose @('up','-d','--wait','db') 'db.log'
 Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','run','./tests/browser') 'fixture.log'
 Invoke-TestCompose @('up','-d','api','model','worker') 'start.log'
 $baseURL='http://127.0.0.1:'+$HttpPort;$ready=$false
 for($i=0;$i-lt40;$i++){try{$health=Invoke-RestMethod -NoProxy -Uri ($baseURL+'/health/ready') -TimeoutSec 2;if($health.status-eq'ready'){$ready=$true;break}}catch{Start-Sleep -Milliseconds 500}}
 if(-not$ready){throw 'API did not become ready.'}
 $env:S2_BROWSER_PASSWORD=$password;$env:S2_BROWSER_BASE_URL=$baseURL;$env:S3_BROWSER_FIXTURE='true'
 Invoke-ProjectWeb -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','test:e2e')
 'PASS: S2 regression and S3 synthetic resume/model browser workflows.'|Set-Content (Join-Path $directory 'browser.log')
 Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','vet','./...') 'vet.log'
 Invoke-TestCompose @('--profile','tests','run','--rm','verifier') 'race.log'
 $race=Get-Content (Join-Path $directory 'race.log') -Raw
 if($race-notmatch'PASS: TestS3RevocationAndResourceChangesBlockBeforeAndAfterModel'-or$race-notmatch'PASS: TestRealPDFTextAndScannedBoundary'){throw 'Required S3 database/PDF tests did not run.'}
 Invoke-TestCompose @('exec','-T','api','pdftotext','-v') 'poppler.log'
 Invoke-TestCompose @('stop','-t','15','api','worker') 'stop.log'
 foreach($service in @('api','worker')){$id=(& docker @compose ps -a -q $service).Trim();$exitCode=(& docker inspect --format '{{.State.ExitCode}}' $id).Trim();if($LASTEXITCODE-ne0-or$exitCode-ne'0'){throw ($service+' did not stop cleanly.')}}
 Invoke-TestCompose @('logs','--no-color','api','worker','model') 'services.log'
 'PASS: S3 isolated simulation, Go race/vet with real PostgreSQL, real Poppler, S2 regression and graceful shutdown. Real DeepSeek acceptance deferred.'|Tee-Object -FilePath (Join-Path $directory 'result.log')
}finally{
 try{
  if($project-notmatch'^job-assistant-s3-verify-[a-f0-9]{8}$'){throw 'Unexpected cleanup target.'}
  & docker @compose --profile tools --profile tests down --volumes --timeout 15
  if($LASTEXITCODE-ne0){throw ('Temporary resources require cleanup: '+$project)}
  'Temporary containers, network and private test volume removed; local project data retained.'|Set-Content (Join-Path $directory 'cleanup.log')
 }finally{foreach($key in $previous.Keys){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}}
}
