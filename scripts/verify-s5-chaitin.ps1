#Requires -Version 7.0
param(
 [ValidateRange(1024,65535)][int]$HttpPort=18084,
 [string]$NodeExecutable,[string]$PnpmCli,[string]$PnpmStore,[string]$WebProjectRoot,[string]$GoProxy='https://proxy.golang.org,direct',
 [switch]$UseSystemProxy
)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'web-tools.ps1')
$root=Split-Path -Parent $PSScriptRoot
if(-not $WebProjectRoot){$WebProjectRoot=$root}
$WebProjectRoot=(Resolve-Path -LiteralPath $WebProjectRoot).Path
$project='job-assistant-s5-chaitin-verify-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$directory=Join-Path $root ('.cache/verification/'+$project)
New-Item -ItemType Directory -Path $directory -Force|Out-Null
function New-TestSecret { [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)) }
$password=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=')
$settings=[ordered]@{VERIFY_DB_PASSWORD=(New-TestSecret).Replace('+','-').Replace('/','_').TrimEnd('=');VERIFY_ADMIN_PASSWORD=$password;VERIFY_CSRF_SECRET=(New-TestSecret);VERIFY_NOTIFICATION_KEY=(New-TestSecret);VERIFY_HTTP_PORT=$HttpPort;VERIFY_HOST_DB_PORT=18087;VERIFY_GOPROXY=$GoProxy}
$envFile=Join-Path $directory 'compose.env'
Set-Content -LiteralPath $envFile -Value @(foreach($key in $settings.Keys){$key+'='+$settings[$key]}) -Encoding utf8NoBOM
$compose=@('compose','--project-name',$project,'--env-file',$envFile,'-f',(Join-Path $root 'deploy/compose.verify.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s2.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s3.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-s4.yaml'),'-f',(Join-Path $root 'deploy/compose.verify-host-collector.yaml'))
$previous=@{}
foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY','S2_BROWSER_PASSWORD','S2_BROWSER_BASE_URL','S3_BROWSER_FIXTURE','S4_BROWSER_FIXTURE')){$previous[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
function Invoke-TestCompose([string[]]$Commands,[string]$LogName){
 & docker @compose @Commands 2>&1|Tee-Object -FilePath (Join-Path $directory $LogName)
 if($LASTEXITCODE-ne0){throw ('S5 Chaitin verification failed: '+$LogName)}
}
try{
 $install=@('install','--frozen-lockfile');if($PnpmStore){$install+=@('--store-dir',$PnpmStore)}
 Invoke-ProjectWeb -ProjectRoot $WebProjectRoot -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands $install
 Invoke-ProjectWeb -ProjectRoot $WebProjectRoot -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('run','build')
 if($WebProjectRoot -ne $root){
  $dist=Join-Path $root 'web/dist';New-Item -ItemType Directory -Path $dist -Force|Out-Null
  Copy-Item -Path (Join-Path $WebProjectRoot 'web/dist/*') -Destination $dist -Recurse -Force
 }
 if($UseSystemProxy){$target=[Uri]'https://registry-1.docker.io';$proxy=[Net.WebRequest]::GetSystemWebProxy().GetProxy($target);if($proxy.Host-eq$target.Host-or$proxy.UserInfo){throw 'System HTTP proxy unavailable.'};$env:HTTP_PROXY=$proxy.AbsoluteUri;$env:HTTPS_PROXY=$proxy.AbsoluteUri;$env:NO_PROXY='localhost,127.0.0.1,::1'}
 Invoke-TestCompose @('--profile','tests','build','api','verifier') 'build.log'
 foreach($key in @('HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
 Invoke-TestCompose @('up','-d','--wait','db') 'db.log'
 Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','vet','./...') 'vet.log'
 Invoke-TestCompose @('--profile','tests','run','--rm','verifier') 'race.log'
 $race=Get-Content (Join-Path $directory 'race.log') -Raw
 if($race-match'--- SKIP:'-or$race-notmatch'PASS: TestPersonalNotificationsRequireCurrentPermissionProfileAndRecommendation'-or$race-notmatch'PASS: TestDailyCollectionPlanningDomainIsolationAndRecovery'-or$race-notmatch'PASS: TestS3RevocationAndResourceChangesBlockBeforeAndAfterModel'-or$race-notmatch'PASS: TestRealPDFTextAndScannedBoundary'-or$race-notmatch'PASS: TestMokaSourcePublicationRepeatFailureAndExistingIsolation'-or$race-notmatch'PASS: TestMokaSourcesShareDomainAndGlobalReadLimits'-or$race-notmatch'PASS: TestZJDITPublicationRepeatsFailuresAndOtherSourceIsolation'-or$race-notmatch'PASS: TestPublicCollectionRuntimeSourceFiltersShareRecovery'-or$race-notmatch'PASS: TestChaitinPublicationVersionsFailuresAndPermissionIsolation'){throw 'Required S3/S4/S5 database/PDF tests did not run.'}
 Invoke-TestCompose @('--profile','tests','run','--rm','--entrypoint','go','verifier','run','./tests/browser') 'fixture.log'
 & (Join-Path $PSScriptRoot 'verify-host-collector.ps1') -DatabaseUrl ('postgres://jobassistant_test:'+$settings.VERIFY_DB_PASSWORD+'@127.0.0.1:18087/jobassistant_test?sslmode=disable') -Directory $directory
 Invoke-TestCompose @('up','-d','api','model','worker') 'start.log'
 $baseURL='http://127.0.0.1:'+$HttpPort;$ready=$false
 for($i=0;$i-lt40;$i++){try{$health=Invoke-RestMethod -NoProxy -Uri ($baseURL+'/health/ready') -TimeoutSec 2;if($health.status-eq'ready'){$ready=$true;break}}catch{Start-Sleep -Milliseconds 500}}
 if(-not$ready){throw 'API did not become ready.'}
 $env:S2_BROWSER_PASSWORD=$password;$env:S2_BROWSER_BASE_URL=$baseURL;$env:S3_BROWSER_FIXTURE='true';$env:S4_BROWSER_FIXTURE='true'
 Invoke-ProjectWeb -ProjectRoot $WebProjectRoot -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('exec','playwright','test','jobs-and-applications.spec.ts','resumes-and-matching.spec.ts','--output',(Join-Path $directory 'regression-browser-results'))
 # The regression suite already consumes nine logins from one host. Start the
 # independent S4 browser suite with a fresh API limiter, preserving DB sessions
 # and all production rate-limit settings.
 Invoke-TestCompose @('restart','api') 'browser-api-restart.log'
 $ready=$false
 for($i=0;$i-lt40;$i++){try{$health=Invoke-RestMethod -NoProxy -Uri ($baseURL+'/health/ready') -TimeoutSec 2;if($health.status-eq'ready'){$ready=$true;break}}catch{Start-Sleep -Milliseconds 500}}
 if(-not$ready){throw 'API did not become ready after browser-suite restart.'}
 Invoke-ProjectWeb -ProjectRoot $WebProjectRoot -NodeExecutable $NodeExecutable -PnpmCli $PnpmCli -Commands @('exec','playwright','test','s4.spec.ts','--output',(Join-Path $directory 'notification-browser-results'))
 'PASS: S2/S3 regression and S4 subscriptions/simulated-delivery browser workflows.'|Set-Content (Join-Path $directory 'browser.log')
 Invoke-TestCompose @('exec','-T','api','pdftotext','-v') 'poppler.log'
 Invoke-TestCompose @('stop','-t','15','api','worker') 'stop.log'
 foreach($service in @('api','worker')){$id=(& docker @compose ps -a -q $service).Trim();$exitCode=(& docker inspect --format '{{.State.ExitCode}}' $id).Trim();if($LASTEXITCODE-ne0-or$exitCode-ne'0'){throw ($service+' did not stop cleanly.')}}
 Invoke-TestCompose @('logs','--no-color','api','worker','model') 'services.log'
 'PASS: S5 Chaitin source integration, real PostgreSQL/Linux race, S2/S3/S4 browser regression and clean shutdown. No real Feishu messages sent.'|Tee-Object -FilePath (Join-Path $directory 'result.log')
}finally{
 try{
  if($project-notmatch'^job-assistant-s5-chaitin-verify-[a-f0-9]{8}$'){throw 'Unexpected cleanup target.'}
  & docker @compose --profile tools --profile tests down --volumes --timeout 15
  if($LASTEXITCODE-ne0){throw ('Temporary resources require cleanup: '+$project)}
  'Temporary containers, network and private test volume removed; local project data retained.'|Set-Content (Join-Path $directory 'cleanup.log')
 }finally{foreach($key in $previous.Keys){[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}}
}
