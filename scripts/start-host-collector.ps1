# Optional local runtime for sources reachable from Windows but not Docker.
# It consumes the same durable queue and never runs private or notification jobs.
param([switch]$Stop)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$directory=Join-Path $root '.cache/local/host-collector'
New-Item -ItemType Directory -Path $directory -Force|Out-Null
$manifest=Join-Path $directory 'process.json'
if(Test-Path -LiteralPath $manifest){
 $previous=Get-Content -LiteralPath $manifest -Raw|ConvertFrom-Json
 $process=Get-Process -Id $previous.pid -ErrorAction SilentlyContinue
 if($process){
  if(-not[string]::Equals($process.Path,$previous.executable,[StringComparison]::OrdinalIgnoreCase)){throw 'Stored collector PID belongs to another program.'}
  Set-Content -LiteralPath $previous.stop_file -Value 'stop' -Encoding utf8NoBOM
  if(-not $process.WaitForExit(15000)){throw 'Host collector did not stop; it was retained.'}
 }
}
if($Stop){Write-Output 'Host public collector stopped.';return}
$settings=@{}
foreach($line in Get-Content -LiteralPath (Join-Path $root '.env')){if($line-match'^([A-Z0-9_]+)=(.*)$'){$settings[$Matches[1]]=$Matches[2]}}
$sources=@($settings.COLLECTOR_HOST_SOURCES-split','|ForEach-Object{$_.Trim()}|Where-Object{$_})
if(-not$sources.Count){Write-Output 'No host collector sources configured.';return}
$database=[Uri]$settings.DATABASE_URL
if($database.Host-notin@('localhost','127.0.0.1')){throw 'Host collector requires the local project database.'}
$binary=Join-Path $directory 'public-worker.exe'
& go -C (Join-Path $root 'backend') build -trimpath -o $binary ./cmd/public-worker
if($LASTEXITCODE-ne0){throw 'Host public collector build failed.'}
$marker=Join-Path $directory ('stop-'+[Guid]::NewGuid().ToString('N'))
$previousEnvironment=@{}
try {
 foreach($key in @('DATABASE_URL','HTTP_PROXY','HTTPS_PROXY','NO_PROXY')){$previousEnvironment[$key]=[Environment]::GetEnvironmentVariable($key,'Process')}
 $env:DATABASE_URL=$settings.DATABASE_URL;$env:HTTP_PROXY='';$env:HTTPS_PROXY='';$env:NO_PROXY='*'
 $process=Start-Process -FilePath $binary -ArgumentList @('-sources',($sources-join','),'-stop-file',('"'+$marker+'"')) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $directory 'stdout.log') -RedirectStandardError (Join-Path $directory 'stderr.log')
 @{pid=$process.Id;executable=$binary;sources=$sources;stop_file=$marker}|ConvertTo-Json|Set-Content -LiteralPath $manifest -Encoding utf8NoBOM
 $started=$false
 for($i=0;$i-lt100;$i++){
  $process.Refresh()
  if($process.HasExited){throw 'Host collector exited during startup.'}
  if((Test-Path -LiteralPath (Join-Path $directory 'stdout.log'))-and(Get-Content -LiteralPath (Join-Path $directory 'stdout.log') -Raw)-match'host public collector started'){$started=$true;break}
  Start-Sleep -Milliseconds 100
 }
 if(-not$started){throw 'Host collector did not become ready; inspect its local logs.'}
 Write-Output ('Host public collector running for '+($sources-join', ')+'.')
}finally{foreach($key in $previousEnvironment.Keys){[Environment]::SetEnvironmentVariable($key,$previousEnvironment[$key],'Process')}}
