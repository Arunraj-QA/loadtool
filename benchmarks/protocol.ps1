# Protocol benchmark harness (Phase 2): runs one LoadTool scenario at
# several levels against the demo API, a fresh server process per run, and
# records LoadTool's and the server's peak memory and CPU, plus the named
# metrics from LoadTool's JSON summary.
#
# A level is a VU count (passed as --vus, with --duration), or, with
# -LevelEnv, a value passed to the script in that environment variable
# (with DURATION_SEC), for scripts whose scenarios set their own VUs.
#
# For each level: one discarded warm-up, then -Runs measured runs, the
# levels alternating within each round. Every run (warm-ups included) is
# written to <OutDir>/runs.jsonl; values that could not be read are left
# empty, never estimated. benchmarks/summarize-protocol.py prints medians.
#
# Prerequisites (from the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   go build -o bin/demo-api.exe ./examples/server
#
# Usage:
#   ./benchmarks/protocol.ps1 -Script benchmarks/loadtool/websocket.ts -Levels 100,500 `
#     -Metrics ws_sessions,ws_msg_latency -OutDir <dir>
#   (in Windows PowerShell, pass a list as -Levels 100,500 inside the script,
#   not through -File, which would read "100,500" as one number)
param(
  [Parameter(Mandatory)] [string] $Script,
  [Parameter(Mandatory)] [int[]] $Levels,
  [Parameter(Mandatory)] [string[]] $Metrics,
  [Parameter(Mandatory)] [string] $OutDir,
  [string] $LevelEnv = '',
  [int] $DurationSec = 30,
  [int] $Runs = 3,
  [int] $WarmupSec = 10,
  [string] $Delay = '10ms',
  [string[]] $ServerArgs = @(),
  [hashtable] $Env = @{},
  [string[]] $ExtraArgs = @(),
  [string] $Server = 'bin/demo-api.exe',
  [string] $LoadTool = 'bin/loadtool.exe'
)

$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
  New-Item -ItemType Directory -Force $OutDir | Out-Null
  $jsonl = Join-Path $OutDir 'runs.jsonl'
  $cores = [Environment]::ProcessorCount
  $script:srv = $null
  try {
    function runOne([int] $level, [string] $round, [int] $secs) {
      $tag = "{0}-{1}" -f $level, $round
      $log = Join-Path $OutDir "$tag.log"
      $summary = Join-Path $OutDir "$tag.json"
      # A fresh server: no state (sessions, Kafka messages) carries over.
      $script:srv = Start-Process -FilePath $Server -ArgumentList (@('-delay', $Delay) + $ServerArgs) -PassThru -WindowStyle Hidden
      Start-Sleep -Seconds 2
      foreach ($k in $Env.Keys) { Set-Item "env:$k" $Env[$k] }
      $runArgs = @('run', ('"' + (Resolve-Path $Script).Path + '"'), '--out', ('"json=' + $summary + '"'))
      if ($LevelEnv) {
        Set-Item "env:$LevelEnv" $level
        $env:DURATION_SEC = $secs
      } else {
        $runArgs += @('--vus', $level, '--duration', "${secs}s")
      }
      $runArgs += $ExtraArgs
      $started = Get-Date
      $p = Start-Process -FilePath $LoadTool -ArgumentList $runArgs -RedirectStandardOutput $log -RedirectStandardError "$log.err" -PassThru -NoNewWindow
      $handle = $p.Handle # keeps the exit code readable
      $peakPriv = 0; $peakWS = 0; $cpu = [TimeSpan]::Zero; $srvPriv = 0
      while (-not $p.HasExited) {
        try { $p.Refresh(); $peakPriv = [math]::Max($peakPriv, $p.PrivateMemorySize64); $peakWS = [math]::Max($peakWS, $p.WorkingSet64); $cpu = $p.TotalProcessorTime } catch {}
        try { $script:srv.Refresh(); $srvPriv = [math]::Max($srvPriv, $script:srv.PrivateMemorySize64) } catch {}
        Start-Sleep -Milliseconds 200
      }
      $p.WaitForExit()
      $wall = ((Get-Date) - $started).TotalSeconds
      $srvCpu = try { $script:srv.Refresh(); $script:srv.TotalProcessorTime.TotalSeconds } catch { $null }
      Stop-Process -Id $script:srv.Id -Force -ErrorAction SilentlyContinue
      $script:srv = $null
      $r = [ordered]@{ script = $Script; level = $level; levelKind = $(if ($LevelEnv) { $LevelEnv } else { 'vus' }); round = $round; durationSec = $secs
        startedAt = $started.ToString('o'); wallSec = [math]::Round($wall, 1); exitCode = $p.ExitCode
        peakPrivateMB = [math]::Round($peakPriv / 1MB, 1); peakWorkingSetMB = [math]::Round($peakWS / 1MB, 1)
        cpuPercentOfMachine = if ($wall -gt 0) { [math]::Round(100 * $cpu.TotalSeconds / $wall / $cores, 1) } else { $null }
        serverPeakPrivateMB = [math]::Round($srvPriv / 1MB, 1)
        serverCpuPercentOfMachine = if ($wall -gt 0 -and $null -ne $srvCpu) { [math]::Round(100 * $srvCpu / $wall / $cores, 1) } else { $null }
        scriptErrors = $null; checksPassed = $null; checksFailed = $null; metrics = [ordered]@{} }
      if (Test-Path $summary) {
        $doc = Get-Content $summary -Raw | ConvertFrom-Json
        $r.scriptErrors = $doc.metrics.script_errors.count
        if ($doc.metrics.checks) { $r.checksPassed = $doc.metrics.checks.passes; $r.checksFailed = $doc.metrics.checks.fails }
        foreach ($m in $Metrics) { if ($doc.metrics.$m) { $r.metrics[$m] = $doc.metrics.$m } }
      }
      $r
    }

    if ($WarmupSec -gt 0) {
      foreach ($l in $Levels) { (runOne $l 'warmup' $WarmupSec | ConvertTo-Json -Compress -Depth 5) | Add-Content -Encoding utf8 $jsonl }
    }
    for ($round = 1; $round -le $Runs; $round++) {
      foreach ($l in $Levels) {
        $r = runOne $l "run$round" $DurationSec
        ($r | ConvertTo-Json -Compress -Depth 5) | Add-Content -Encoding utf8 $jsonl
        '{0,6} {1,-6} peak private {2,8} MB  CPU {3,5}%  server {4,7} MB {5,5}%  script errors {6}  exit {7}' -f $r.level, $r.round, $r.peakPrivateMB, $r.cpuPercentOfMachine, $r.serverPeakPrivateMB, $r.serverCpuPercentOfMachine, $r.scriptErrors, $r.exitCode
        Start-Sleep -Seconds 3
      }
    }
  } finally {
    if ($script:srv) { Stop-Process -Id $script:srv.Id -Force -ErrorAction SilentlyContinue }
  }
} finally {
  Pop-Location
}
