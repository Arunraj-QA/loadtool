# Phase 2 benchmark session (2026-10-14): the exit criteria (2 and 3)
# first, then each protocol at several levels. Run from the repository
# root, on AC power, with the machine otherwise idle:
#
#   go build -o bin/loadtool.exe ./cmd/loadtool          # this commit
#   go build -o bin/loadtool-p1.exe ./cmd/loadtool       # in a worktree at cf35298
#   go build -o bin/demo-api.exe ./examples/server
#   go build -o bin/benchserver.exe ./benchmarks/server
#   go test -c -o bin/script-p2.test.exe ./internal/script     # this commit
#   go test -c -o bin/script-p1.test.exe ./internal/script     # at cf35298
#   ./benchmarks/results/2026-10-14-phase2/run.ps1 -JMeterHome <JMeter 5.6.3 without plugins>
#
# -Sections runs only some parts, for example -Sections ab first, to check
# for an HTTP regression before running the rest.
param(
  [Parameter(Mandatory)] [string] $JMeterHome,
  [ValidateSet('ab', 'jmeter', 'vumem', 'http', 'websocket', 'grpc', 'graphql', 'kafka', 'pipeline')]
  [string[]] $Sections = @('ab', 'jmeter', 'vumem', 'http', 'websocket', 'grpc', 'graphql', 'kafka', 'pipeline'),
  [string] $Out = 'benchmarks/results/2026-10-14-phase2/raw'
)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path (Split-Path (Split-Path $PSScriptRoot -Parent) -Parent) -Parent)
New-Item -ItemType Directory -Force $Out | Out-Null
function note([string] $msg) { "{0:HH:mm:ss} {1}" -f (Get-Date), $msg | Tee-Object -Append -FilePath "$Out/session.log" }
function cpuSamples { ((Get-Counter '\Processor(_Total)\% Processor Time' -SampleInterval 5 -MaxSamples 6).CounterSamples.CookedValue | ForEach-Object { [math]::Round($_, 0) }) -join ', ' }

# 0. Environment.
$envInfo = [ordered]@{
  commit = (git rev-parse --short HEAD); phase1Build = 'cf35298'
  go = (go version); machine = (Get-CimInstance Win32_Processor).Name
  powerOnline = (Get-CimInstance -Namespace root/wmi -ClassName BatteryStatus).PowerOnline
  backgroundCpuBefore = (cpuSamples)
}
$envInfo | ConvertTo-Json | Set-Content -Encoding utf8 "$Out/environment.json"
note "start: $($envInfo | ConvertTo-Json -Compress)"

# 1. Exit criterion 2: Phase 1 build against this build, HTTP at 1,000
# VUs, 60 s, 3 rounds alternating the order, as the Phase 1 A/B.
if ($Sections -contains 'ab') {
note 'A/B: Phase 1 (cf35298) vs Phase 2 at 1,000 VUs'
$srv = Start-Process bin/benchserver.exe -ArgumentList '-addr', '127.0.0.1:8080', '-delay', '10ms' -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
$builds = @(@{ name = 'phase1'; exe = 'bin/loadtool-p1.exe' }, @{ name = 'phase2'; exe = 'bin/loadtool.exe' })
$csv = "$Out/ab-phase1-vs-phase2.csv"
'build,round,peakPrivateMB,peakWorkingSetMB,cpuPercentOfMachine,requests,reqPerSec,errors,exit' | Set-Content -Encoding utf8 $csv
try {
  for ($r = 1; $r -le 3; $r++) {
    $order = if ($r % 2) { $builds } else { @($builds[1], $builds[0]) }
    foreach ($b in $order) {
      $log = "$Out/ab-$($b.name)-run$r.txt"
      $p = Start-Process $b.exe -ArgumentList 'run', ('"' + (Resolve-Path benchmarks/loadtool/scenario.ts).Path + '"'), '--vus', 1000, '--duration', '60s' -PassThru -WindowStyle Hidden -RedirectStandardOutput $log -RedirectStandardError "$log.err"
      $null = $p.Handle
      $started = Get-Date
      $ws = 0; $pv = 0; $cpu = [TimeSpan]::Zero
      while (-not $p.HasExited) {
        try { $p.Refresh(); $ws = [math]::Max($ws, $p.WorkingSet64); $pv = [math]::Max($pv, $p.PrivateMemorySize64); $cpu = $p.TotalProcessorTime } catch {}
        Start-Sleep -Milliseconds 250
      }
      $p.WaitForExit()
      $cpuPct = 100 * $cpu.TotalSeconds / ((Get-Date) - $started).TotalSeconds / [Environment]::ProcessorCount
      $txt = Get-Content $log -Raw
      $req = if ($txt -cmatch 'Requests:\s+([\d,]+)') { $Matches[1] -replace ',', '' } else { '' }
      $rps = if ($txt -cmatch 'Requests:\s+[\d,]+ \(([\d,.]+) req/s\)') { $Matches[1] -replace ',', '' } else { '' }
      $err = if ($txt -cmatch 'Errors:\s+([\d,]+)') { $Matches[1] -replace ',', '' } else { '' }
      $line = '{0},{1},{2:F1},{3:F1},{4:F1},{5},{6},{7},{8}' -f $b.name, $r, ($pv / 1MB), ($ws / 1MB), $cpuPct, $req, $rps, $err, $p.ExitCode
      $line | Add-Content -Encoding utf8 $csv
      note "A/B $line"
      Start-Sleep -Seconds 3
    }
  }
} finally { Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue }
}

# 2. Exit criterion 2 (memory below JMeter's): the Phase 0/1 harness and
# settings at 1,000 VUs.
if ($Sections -contains 'jmeter') {
note 'LoadTool vs JMeter at 1,000 VUs'
& ./benchmarks/measure.ps1 -Tools loadtool, jmeter -VUs 1000 -DurationSec 60 -Runs 3 -WarmupSec 30 -JMeterHome $JMeterHome -OutDir "$Out/vs-jmeter" *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 3. Exit criterion 3: retained memory per VU, 5 rounds alternating.
if ($Sections -contains 'vumem') {
note 'BenchmarkVURetainedMemory: Phase 1 vs Phase 2'
for ($r = 1; $r -le 5; $r++) {
  foreach ($b in @(@{ n = 'phase1'; exe = 'bin/script-p1.test.exe' }, @{ n = 'phase2'; exe = 'bin/script-p2.test.exe' })) {
    "== $($b.n) round $r" | Add-Content -Encoding utf8 "$Out/vu-retained-memory.txt"
    & $b.exe '-test.run' '^$' '-test.bench' 'VURetainedMemory' '-test.benchtime' '1x' 2>&1 | Add-Content -Encoding utf8 "$Out/vu-retained-memory.txt"
  }
}
}

# 4. HTTP at 100 to 5,000 VUs.
if ($Sections -contains 'http') {
note 'HTTP sweep'
& ./benchmarks/measure.ps1 -Tools loadtool -VUs 100, 500, 1000, 5000 -DurationSec 30 -Runs 3 -WarmupSec 10 -OutDir "$Out/http" *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 5. WebSocket: held connections, one message a second each.
if ($Sections -contains 'websocket') {
note 'WebSocket sweep'
& ./benchmarks/protocol.ps1 -Script benchmarks/loadtool/websocket.ts -Levels 100, 500, 1000, 5000 -OutDir "$Out/websocket" `
  -Metrics ws_sessions, ws_session_failed, ws_connecting, ws_msgs_sent, ws_msgs_received, ws_msg_latency, ws_errors `
  -ServerArgs @('-grpc-addr', '""', '-kafka-port', '0') -ExtraArgs @('--graceful-stop', '1s') *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 6. gRPC unary calls.
if ($Sections -contains 'grpc') {
note 'gRPC sweep'
& ./benchmarks/grpc-unary.ps1 -VUs 100, 500, 1000, 5000 -OutDir "$Out/grpc" *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 7. GraphQL queries.
if ($Sections -contains 'graphql') {
note 'GraphQL sweep'
& ./benchmarks/protocol.ps1 -Script benchmarks/loadtool/graphql.ts -Levels 100, 500, 1000 -OutDir "$Out/graphql" `
  -Metrics graphql_reqs, graphql_req_duration, graphql_req_failed `
  -ServerArgs @('-grpc-addr', '""', '-kafka-port', '0') *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 8. Kafka: produce throughput with 1 KiB messages (closed loop).
if ($Sections -contains 'kafka') {
note 'Kafka produce, 1 KiB'
& ./benchmarks/protocol.ps1 -Script benchmarks/loadtool/kafka-produce.ts -Levels 10, 100, 1000 -OutDir "$Out/kafka-produce" `
  -Metrics kafka_messages_produced, kafka_produce_duration, kafka_produce_failed -Env @{ VALUE_BYTES = '1024' } `
  -ServerArgs @('-grpc-addr', '""') *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

# 9. Kafka: producer and consumers at fixed rates, 1 KiB messages.
if ($Sections -contains 'pipeline') {
note 'Kafka pipeline, 1 KiB'
& ./benchmarks/protocol.ps1 -Script benchmarks/loadtool/kafka-pipeline.ts -Levels 1000, 5000, 20000 -LevelEnv RATE -OutDir "$Out/kafka-pipeline" `
  -Metrics kafka_messages_produced, kafka_produce_duration, kafka_produce_failed, kafka_messages_consumed, kafka_consume_latency, kafka_consume_failed, dropped_iterations `
  -Env @{ VALUE_BYTES = '1024' } -ServerArgs @('-grpc-addr', '""') *>&1 | Tee-Object -Append -FilePath "$Out/session.log"
}

note "end; background CPU after: $(cpuSamples)"
