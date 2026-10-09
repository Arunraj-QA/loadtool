# Kafka produce benchmark (ADR-022): LoadTool at several VU counts, each VU
# with its own producer, against the demo API's in-process broker (kfake,
# franz-go's fake cluster; not a real Kafka). Each run gets a fresh broker
# process, because kfake keeps every message in memory; its peak memory is
# sampled too.
#
# For each VU level: one discarded warm-up, then -Runs measured runs, the
# levels alternating within each round. Every run (warm-ups included) is
# written to <OutDir>/runs.jsonl, with values read from LoadTool's JSON
# summary and from sampling the process; values that could not be read
# are left empty, never estimated.
#
# Prerequisites (from the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   go build -o bin/demo-api.exe ./examples/server
#
# Usage:
#   ./benchmarks/kafka-produce.ps1                       # 10, 100, 500, 1000 VUs
#   ./benchmarks/kafka-produce.ps1 -VUs 10 -DurationSec 3 -Runs 1 -WarmupSec 0   # smoke test
param(
  [int[]] $VUs = @(10, 100, 500, 1000),
  [int] $DurationSec = 30,
  [int] $Runs = 3,
  [int] $WarmupSec = 10,
  [int] $KafkaPort = 9092,
  [string] $Server = 'bin/demo-api.exe',
  [string] $LoadTool = 'bin/loadtool.exe',
  [string] $Script = 'benchmarks/loadtool/kafka-produce.ts',
  [string] $OutDir = ('benchmarks/results/{0}-kafka/raw' -f (Get-Date -Format 'yyyy-MM-dd'))
)

$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
  New-Item -ItemType Directory -Force $OutDir | Out-Null
  $jsonl = Join-Path $OutDir 'runs.jsonl'
  $cores = [Environment]::ProcessorCount
  $script:srv = $null
  try {

    function runOne([int] $vus, [string] $round, [int] $secs) {
      $tag = "{0}vus-{1}" -f $vus, $round
      $log = Join-Path $OutDir "$tag.log"
      $summary = Join-Path $OutDir "$tag.json"
      # A fresh broker; the HTTP side listens on a port of its own, unused here.
      $script:srv = Start-Process -FilePath $Server -ArgumentList @('-addr', '127.0.0.1:18095', '-grpc-addr', '""', '-kafka-port', $KafkaPort) -PassThru -WindowStyle Hidden
      Start-Sleep -Seconds 2
      $env:KAFKA_BROKERS = "127.0.0.1:$KafkaPort"
      $runArgs = @('run', ('"' + (Resolve-Path $Script).Path + '"'), '--vus', $vus, '--duration', "${secs}s", '--out', ('"json=' + $summary + '"'))
      $started = Get-Date
      $p = Start-Process -FilePath $LoadTool -ArgumentList $runArgs -RedirectStandardOutput $log -RedirectStandardError "$log.err" -PassThru -NoNewWindow
      $handle = $p.Handle # keeps the exit code readable
      $peakPriv = 0; $peakWS = 0; $cpu = [TimeSpan]::Zero; $brokerPriv = 0
      while (-not $p.HasExited) {
        try { $p.Refresh(); $peakPriv = [math]::Max($peakPriv, $p.PrivateMemorySize64); $peakWS = [math]::Max($peakWS, $p.WorkingSet64); $cpu = $p.TotalProcessorTime } catch {}
        try { $script:srv.Refresh(); $brokerPriv = [math]::Max($brokerPriv, $script:srv.PrivateMemorySize64) } catch {}
        Start-Sleep -Milliseconds 200
      }
      $p.WaitForExit()
      $wall = ((Get-Date) - $started).TotalSeconds
      $brokerCpu = try { $script:srv.Refresh(); $script:srv.TotalProcessorTime.TotalSeconds } catch { $null }
      Stop-Process -Id $script:srv.Id -Force -ErrorAction SilentlyContinue
      $script:srv = $null
      $r = [ordered]@{ vus = $vus; round = $round; durationSec = $secs; startedAt = $started.ToString('o'); exitCode = $p.ExitCode
        peakPrivateMB = [math]::Round($peakPriv / 1MB, 1); peakWorkingSetMB = [math]::Round($peakWS / 1MB, 1)
        cpuPercentOfMachine = if ($wall -gt 0) { [math]::Round(100 * $cpu.TotalSeconds / $wall / $cores, 1) } else { $null }
        brokerPeakPrivateMB = [math]::Round($brokerPriv / 1MB, 1)
        brokerCpuPercentOfMachine = if ($wall -gt 0 -and $null -ne $brokerCpu) { [math]::Round(100 * $brokerCpu / $wall / $cores, 1) } else { $null }
        messages = $null; messagesPerSec = $null; failed = $null; avgMs = $null; p50Ms = $null; p95Ms = $null; p99Ms = $null; maxMs = $null; scriptErrors = $null }
      if (Test-Path $summary) {
        $m = (Get-Content $summary -Raw | ConvertFrom-Json).metrics
        if ($m.kafka_messages_produced) { $r.messages = $m.kafka_messages_produced.count; $r.messagesPerSec = [math]::Round($m.kafka_messages_produced.rate, 1) }
        if ($m.kafka_produce_failed) { $r.failed = $m.kafka_produce_failed.trues }
        if ($m.kafka_produce_duration) { $d = $m.kafka_produce_duration; $r.avgMs = $d.avg; $r.p50Ms = $d.p50; $r.p95Ms = $d.p95; $r.p99Ms = $d.p99; $r.maxMs = $d.max }
        $r.scriptErrors = $m.script_errors.count
      }
      $r
    }

    if ($WarmupSec -gt 0) {
      foreach ($v in $VUs) { (runOne $v 'warmup' $WarmupSec | ConvertTo-Json -Compress) | Add-Content -Encoding utf8 $jsonl }
    }
    for ($round = 1; $round -le $Runs; $round++) {
      foreach ($v in $VUs) {
        $r = runOne $v "run$round" $DurationSec
        ($r | ConvertTo-Json -Compress) | Add-Content -Encoding utf8 $jsonl
        '{0,5} VUs {1,-5} msgs/s {2,9}  p50 {3,7} p95 {4,7} p99 {5,7} ms  failed {6}  peak private {7} MB  CPU {8}%  broker {9} MB {10}%  exit {11}' -f $r.vus, $r.round, $r.messagesPerSec, $r.p50Ms, $r.p95Ms, $r.p99Ms, $r.failed, $r.peakPrivateMB, $r.cpuPercentOfMachine, $r.brokerPeakPrivateMB, $r.brokerCpuPercentOfMachine, $r.exitCode
        Start-Sleep -Seconds 3
      }
    }
  } finally {
    if ($script:srv) { Stop-Process -Id $script:srv.Id -Force -ErrorAction SilentlyContinue }
  }
} finally {
  Pop-Location
}
