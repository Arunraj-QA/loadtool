# A/B of two LoadTool builds on the HTTP exit scenario (as Phase 2
# criterion 2): 1,000 VUs, 60 s, 3 rounds alternating the order, against
# benchmarks/server with a 10 ms delay. Run from the repository root on AC
# power with the machine otherwise idle:
#
#   ./benchmarks/results/2026-10-21-ui/ab.ps1 -Base bin/loadtool-base.exe -New bin/loadtool.exe
param(
  [Parameter(Mandatory)] [string] $Base,
  [Parameter(Mandatory)] [string] $New,
  [int] $VUs = 1000, [int] $Sec = 60, [int] $Rounds = 3,
  [string] $Out = 'benchmarks/results/2026-10-21-ui/raw-ab'
)
$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force $Out | Out-Null
function cpuSamples { ((Get-Counter '\Processor(_Total)\% Processor Time' -SampleInterval 5 -MaxSamples 6).CounterSamples.CookedValue | ForEach-Object { [math]::Round($_, 0) }) -join ', ' }
Add-Type -AssemblyName System.Windows.Forms
[ordered]@{
  commit = (git rev-parse --short HEAD); base = $Base; new = $New; go = (go version)
  power = [string][System.Windows.Forms.SystemInformation]::PowerStatus.PowerLineStatus
  backgroundCpuBefore = (cpuSamples)
} | ConvertTo-Json | Set-Content -Encoding utf8 "$Out/environment.json"

$srv = Start-Process bin/benchserver.exe -ArgumentList '-addr', '127.0.0.1:8080', '-delay', '10ms' -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
$builds = @(@{ name = 'base'; exe = $Base }, @{ name = 'new'; exe = $New })
$csv = "$Out/ab.csv"
'build,round,peakPrivateMB,peakWorkingSetMB,cpuPercentOfMachine,requests,reqPerSec,errors,exit' | Set-Content -Encoding utf8 $csv
$script = '"' + (Resolve-Path benchmarks/loadtool/scenario.ts).Path + '"'
try {
  for ($r = 1; $r -le $Rounds; $r++) {
    $order = if ($r % 2) { $builds } else { @($builds[1], $builds[0]) }
    foreach ($b in $order) {
      $log = "$Out/$($b.name)-run$r.txt"
      $p = Start-Process $b.exe -ArgumentList 'run', $script, '--vus', $VUs, '--duration', "${Sec}s" -PassThru -WindowStyle Hidden -RedirectStandardOutput $log -RedirectStandardError "$log.err"
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
      '{0},{1},{2:F1},{3:F1},{4:F1},{5},{6},{7},{8}' -f $b.name, $r, ($pv / 1MB), ($ws / 1MB), $cpuPct, $req, $rps, $err, $p.ExitCode | Add-Content -Encoding utf8 $csv
      Start-Sleep -Seconds 3
    }
  }
} finally { Stop-Process -Id $srv.Id -Force -ErrorAction SilentlyContinue }
"background CPU after: $(cpuSamples)" | Add-Content -Encoding utf8 "$Out/environment.json.after.txt"
Get-Content $csv
