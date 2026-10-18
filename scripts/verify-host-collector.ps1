param([Parameter(Mandatory)][string]$DatabaseUrl,[Parameter(Mandatory)][string]$Directory)
$ErrorActionPreference='Stop'
$root=Split-Path -Parent $PSScriptRoot
$binary=Join-Path $Directory 'public-worker.exe'
& go -C (Join-Path $root 'backend') build -trimpath -o $binary ./cmd/public-worker
if($LASTEXITCODE-ne0){throw 'Native collector test build failed'}
$marker=Join-Path $Directory 'native-stop'
if(Test-Path -LiteralPath $marker){throw 'Native lifecycle marker already exists'}
$old=[Environment]::GetEnvironmentVariable('DATABASE_URL','Process')
$process=$null
try{
 $env:DATABASE_URL=$DatabaseUrl
 $process=Start-Process -FilePath $binary -ArgumentList @('-sources','zjdit-careers','-stop-file',('"'+$marker+'"')) -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $Directory 'native-stdout.log') -RedirectStandardError (Join-Path $Directory 'native-stderr.log')
 $started=$false
 for($i=0;$i-lt100;$i++){
  $process.Refresh()
  if($process.HasExited){throw 'Native public collector exited during initialization'}
  if((Test-Path -LiteralPath (Join-Path $Directory 'native-stdout.log'))-and(Get-Content (Join-Path $Directory 'native-stdout.log') -Raw)-match'host public collector started'){$started=$true;break}
  Start-Sleep -Milliseconds 100
 }
 if(-not$started){throw 'Native public collector did not start'}
 Set-Content -LiteralPath $marker -Value stop -Encoding utf8NoBOM
 if(-not$process.WaitForExit(15000)-or$process.ExitCode-ne0){throw 'Native public collector did not shut down cleanly'}
 'PASS: native public collector started against the isolated PostgreSQL database and stopped with exit code 0.'|Tee-Object -FilePath (Join-Path $Directory 'native-result.log')
}finally{
 if($process){$process.Refresh();if(-not$process.HasExited){Set-Content -LiteralPath $marker -Value stop -Encoding utf8NoBOM;$process.WaitForExit(15000)|Out-Null}}
 [Environment]::SetEnvironmentVariable('DATABASE_URL',$old,'Process')
}
