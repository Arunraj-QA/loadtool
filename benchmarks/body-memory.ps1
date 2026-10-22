# Large-response memory benchmark (ADR-013): peak memory of LoadTool at a
# VU count against GET /api/large, whose body size is set by -LargeSize.
#
# Each case is "label|loadtool binary|script". Cases run in turn, -Runs
# rounds, after one discarded warm-up per case. Every run, warm-ups
# included, is written to <OutDir>/runs.jsonl; values that could not be
# read are left empty, never estimated.
#
# Prerequisites (from the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   go build -o bin/benchserver.exe ./benchmarks/server
#
# Usage:
#   ./benchmarks/body-memory.ps1                                   # default cases
#   ./benchmarks/body-memory.ps1 -Cases 'old|bin/old.exe|benchmarks/loadtool/large-body.ts'
#   ./benchmarks/body-memory.ps1 -VUs 10 -DurationSec 3 -Runs 1 -WarmupSec 0   # smoke test
param(
  [string[]] $Cases = @(
    'default|bin/loadtool.exe|benchmarks/loadtool/large-body.ts',
    'keep-bodies|bin/loadtool.exe|benchmarks/loadtool/large-body-keep.ts'
  ),
  [int] $VUs = 1000,
  [int] $DurationSec = 30,
  [int] $Runs = 3,
  [int] $WarmupSec = 10,
  [int] $LargeSize = 1048576,
  [string] $ServerAddr = '127.0.0.1:8080',
  [string] $Server = 'bin/benchserver.exe',
  [string] $OutDir = ('benchmarks/results/{0}-body-memory/raw' -f (Get-Date -Format 'yyyy-MM-dd'))
)

$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
  New-Item -ItemType Directory -Force $OutDir | Out-Null
  $jsonl = Join-Path $OutDir 'runs.jsonl'

  $srv = Start-Process -FilePath $Server -ArgumentList @('-addr', $ServerAddr, '-large-size', $LargeSize) -PassThru -WindowStyle Hidden
  try {
    $up = $false
    for ($i = 0; $i -lt 50 -and -not $up; $i++) {
      try { Invoke-WebRequest -UseBasicParsing "http://$ServerAddr/health" | Out-Null; $up = $true } catch { Start-Sleep -Milliseconds 100 }
    }
    if (-not $up) { throw "the benchmark server did not start" }

    # runOne runs one case and returns its measurements.
    function runOne([string] $case, [string] $round, [int] $secs) {
      $label, $bin, $script = $case.Split('|')
      $log = Join-Path $OutDir ("{0}-{1}.log" -f $label, $round)
      # BASE_URL goes through the environment (which __ENV includes), not
      # -e, so builds without -e (Phase 0) run too.
      $env:BASE_URL = "http://$ServerAddr"
      $runArgs = @('run', ('"' + (Resolve-Path $script).Path + '"'), '--vus', $VUs, '--duration', "${secs}s")
      $started = Get-Date
      $p = Start-Process -FilePath $bin -ArgumentList $runArgs -RedirectStandardOutput $log -RedirectStandardError "$log.err" -PassThru -NoNewWindow
      $handle = $p.Handle # keeps the exit code readable after exit
      $peakPriv = 0; $peakWS = 0
      while (-not $p.HasExited) {
        try { $p.Refresh(); $peakPriv = [math]::Max($peakPriv, $p.PrivateMemorySize64); $peakWS = [math]::Max($peakWS, $p.WorkingSet64) } catch {}
        Start-Sleep -Milliseconds 200
      }
      $p.WaitForExit()
      $m = Select-String -Path $log -Pattern 'Requests:\s+([\d,]+)' | Select-Object -First 1
      $requests = if ($m) { [int64]($m.Matches[0].Groups[1].Value -replace ',', '') } else { $null }
      [ordered]@{
        label = $label; round = $round; vus = $VUs; durationSec = $secs; largeSize = $LargeSize
        startedAt = $started.ToString('o'); exitCode = $p.ExitCode; requests = $requests
        peakPrivateMB = if ($peakPriv) { [math]::Round($peakPriv / 1MB, 1) } else { $null }
        peakWorkingSetMB = if ($peakWS) { [math]::Round($peakWS / 1MB, 1) } else { $null }
      }
    }

    if ($WarmupSec -gt 0) {
      foreach ($c in $Cases) {
        $r = runOne $c 'warmup' $WarmupSec
        ($r | ConvertTo-Json -Compress) | Add-Content -Encoding utf8 $jsonl
      }
    }
    for ($round = 1; $round -le $Runs; $round++) {
      foreach ($c in $Cases) {
        $r = runOne $c "run$round" $DurationSec
        ($r | ConvertTo-Json -Compress) | Add-Content -Encoding utf8 $jsonl
        '{0,-14} {1,-6} peak private {2,8} MB  peak WS {3,8} MB  requests {4}  exit {5}' -f $r.label, $r.round, $r.peakPrivateMB, $r.peakWorkingSetMB, $r.requests, $r.exitCode
        Start-Sleep -Seconds 3
      }
    }
  } finally {
    Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue
  }
} finally {
  Pop-Location
}
