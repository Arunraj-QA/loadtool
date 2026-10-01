# Phase 0 benchmark harness: LoadTool, k6 and JMeter against the same local
# benchmark server and equivalent workload (see benchmarks/README.md).
#
# For each VU level and tool: one discarded warm-up run, then measured runs
# that alternate between tools. Every run (warm-ups included) is written to
# <OutDir>/runs.jsonl; missing values are left empty, never estimated.
#
# Prerequisites (from the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   go build -o bin/benchserver.exe ./benchmarks/server
#   k6 installed; JMeter unpacked and -JMeterHome (or JMETER_HOME) set
#
# Usage:
#   ./benchmarks/measure.ps1                                  # full Phase 0 matrix
#   ./benchmarks/measure.ps1 -VUs 10 -DurationSec 5 -Runs 1 -WarmupSec 0   # smoke test
param(
  [ValidateSet('loadtool', 'k6', 'jmeter')]
  [string[]] $Tools = @('loadtool', 'k6', 'jmeter'),
  [int[]] $VUs = @(100, 250, 500, 750, 1000),
  [int] $DurationSec = 60,
  [int] $Runs = 3,
  [int] $WarmupSec = 30,
  [int] $CooldownSec = 5,
  [string] $ServerAddr = '127.0.0.1:8080',
  [string] $ServerDelay = '10ms',
  [string] $Server = 'bin/benchserver.exe',
  [string] $LoadTool = 'bin/loadtool.exe',
  # The real k6 executable; a Chocolatey launcher would hide the process
  # that does the work, so the real k6.exe is looked up when this is empty.
  [string] $K6 = '',
  [string] $JMeterHome = $env:JMETER_HOME,
  [string] $OutDir = ('benchmarks/results/{0}-phase0/raw' -f (Get-Date -Format 'yyyy-MM-dd')),
  # Large per-request JMeter result files go here (git-ignored).
  [string] $BulkDir = 'bench-out/jtl'
)

$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {

$inv = [Globalization.CultureInfo]::InvariantCulture
$target = "http://$ServerAddr/api/test"
$serverHost, $serverPort = $ServerAddr.Split(':')
$logicalCPUs = (Get-CimInstance Win32_Processor | Measure-Object NumberOfLogicalProcessors -Sum).Sum
New-Item -ItemType Directory -Force $OutDir, $BulkDir | Out-Null
$runsFile = Join-Path $OutDir 'runs.jsonl'
$utf8 = New-Object System.Text.UTF8Encoding($false)  # no BOM, for JSON readers
function Write-Utf8([string] $path, [string] $text, [switch] $Append) {
  $full = Join-Path (Get-Location) $path
  if ($Append) { [IO.File]::AppendAllText($full, $text, $utf8) } else { [IO.File]::WriteAllText($full, $text, $utf8) }
}

# JMeter writes one row per request; parsing 500k+ rows in PowerShell is
# slow, so this small reader computes the statistics in C#.
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.IO;
public class JtlStats {
    public long Requests; public long Errors; public double SpanSec;
    public double P50; public double P95; public double P99;
    public static JtlStats Read(string path) {
        using (var r = new StreamReader(path)) {
            string[] h = r.ReadLine().Split(',');
            int iTs = Array.IndexOf(h, "timeStamp"), iEl = Array.IndexOf(h, "elapsed"), iOk = Array.IndexOf(h, "success");
            if (iTs < 0 || iEl < 0 || iOk < 0) throw new Exception("JTL lacks timeStamp/elapsed/success columns: " + string.Join(",", h));
            var el = new List<int>(); long minTs = long.MaxValue, maxEnd = long.MinValue, errors = 0; string line;
            while ((line = r.ReadLine()) != null) {
                if (line.Length == 0) continue;
                string[] f = line.Split(',');
                long ts = long.Parse(f[iTs]); int e = int.Parse(f[iEl]);
                el.Add(e);
                if (ts < minTs) minTs = ts;
                if (ts + e > maxEnd) maxEnd = ts + e;
                if (!string.Equals(f[iOk], "true", StringComparison.OrdinalIgnoreCase)) errors++;
            }
            int[] a = el.ToArray(); Array.Sort(a);
            var s = new JtlStats { Requests = a.Length, Errors = errors };
            if (a.Length > 0) {
                s.SpanSec = (maxEnd - minTs) / 1000.0;
                s.P50 = Rank(a, 50); s.P95 = Rank(a, 95); s.P99 = Rank(a, 99);
            }
            return s;
        }
    }
    // Nearest-rank percentile, the same method LoadTool uses.
    static double Rank(int[] sorted, int p) {
        int rank = (int)(((long)p * sorted.Length + 99) / 100);
        return sorted[Math.Max(rank, 1) - 1];
    }
}
'@

function Resolve-K6 {
  if ($K6) { return $K6 }
  $real = Get-ChildItem 'C:\ProgramData\chocolatey\lib\k6\tools\*\k6.exe' -ErrorAction SilentlyContinue |
    Select-Object -First 1
  if ($real) { return $real.FullName }
  return (Get-Command k6).Source
}

function Get-Parsed([string] $value) {
  return [double]::Parse($value, $inv)
}

# LoadTool prints durations such as "10.72ms", "450.00us" (micro sign) or "1.20s".
function ConvertTo-Ms([string] $value, [string] $unit) {
  $v = Get-Parsed $value
  switch ($unit) { 's' { return $v * 1000 } 'ms' { return $v } default { return $v / 1000 } }
}

function Get-Environment {
  $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
  $os = Get-CimInstance Win32_OperatingSystem
  $battery = Get-CimInstance Win32_Battery -ErrorAction SilentlyContinue | Select-Object -First 1
  $jmeterVersion = $null
  if ($JMeterHome) { $jmeterVersion = Split-Path $JMeterHome -Leaf }
  $javaVersion = (cmd /c 'java -version 2>&1' | Select-Object -First 1)
  [ordered]@{
    capturedAt      = (Get-Date).ToString('o')
    cpu             = $cpu.Name.Trim()
    cores           = $cpu.NumberOfCores
    logicalCPUs     = $logicalCPUs
    ramGB           = [Math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1GB, 1)
    os              = "$($os.Caption) $($os.Version) (build $($os.BuildNumber))"
    onACPower       = if ($battery) { $battery.BatteryStatus -eq 2 } else { $null }
    loadtoolCommit  = (git rev-parse --short HEAD)
    goVersion       = (go version)
    k6Version       = (& (Resolve-K6) version | Select-Object -First 1)
    jmeterVersion   = $jmeterVersion
    javaVersion     = $javaVersion
    target          = $target
    serverDelay     = $ServerDelay
    vus             = $VUs
    durationSec     = $DurationSec
    runs            = $Runs
    warmupSec       = $WarmupSec
    cooldownSec     = $CooldownSec
  }
}

function Get-Command-For([string] $tool, [int] $vus, [int] $seconds, [string] $id) {
  switch ($tool) {
    'loadtool' {
      # Same scenario with TARGET substituted, so all tools hit $target.
      $scenario = Join-Path $OutDir "$id-scenario.ts"
      (Get-Content -Raw -Encoding UTF8 'benchmarks/loadtool/scenario.ts') -replace
        'const TARGET = "[^"]*";', "const TARGET = `"$target`";" |
        Set-Content -Encoding UTF8 $scenario
      return @{ Exe = $LoadTool; Args = "run $scenario --vus $vus --duration ${seconds}s" }
    }
    'k6' {
      $json = Join-Path $OutDir "$id-k6-summary.json"
      return @{
        Exe = (Resolve-K6); Summary = $json
        Args = "run --quiet --vus $vus --duration ${seconds}s --summary-trend-stats med,p(95),p(99) " +
               "--summary-export $json -e TARGET=$target benchmarks/k6/scenario.js"
      }
    }
    'jmeter' {
      if (-not $JMeterHome) { throw 'set -JMeterHome or JMETER_HOME to the unpacked JMeter directory' }
      $jtl = Join-Path $BulkDir "$id.jtl"
      # Keep only the columns the statistics need; JMeter writes less to disk.
      $save = 'label response_message thread_name data_type assertion_results_failure_message ' +
              'bytes sent_bytes thread_counts url latency idle_time connect_time subresults'
      $props = ($save.Split(' ') | ForEach-Object { "-Jjmeter.save.saveservice.$_=false" }) -join ' '
      return @{
        # jmeter.bat applies JMeter's default JVM settings; the java child is measured.
        Exe = (Join-Path $JMeterHome 'bin\jmeter.bat'); Child = 'java.exe'; Jtl = $jtl
        Args = "-n -t benchmarks/jmeter/scenario.jmx -l $jtl -j $(Join-Path $OutDir "$id-jmeter.log") " +
               "-Jvus=$vus -Jduration=$seconds -Jhost=$serverHost -Jport=$serverPort " +
               "-Jjmeter.save.saveservice.output_format=csv $props"
      }
    }
  }
}

# Waits for the process started by a launcher (jmeter.bat) to appear.
function Find-Child([int] $parentId, [string] $name) {
  for ($i = 0; $i -lt 100; $i++) {
    $c = Get-CimInstance Win32_Process -Filter "ParentProcessId=$parentId AND Name='$name'" | Select-Object -First 1
    if ($c) { return Get-Process -Id $c.ProcessId }
    Start-Sleep -Milliseconds 100
  }
  throw "no $name child process of $parentId appeared"
}

function Read-Result([string] $tool, $cmd, [string] $stdout) {
  $r = @{ Requests = $null; RequestsPerSec = $null; P50Ms = $null; P95Ms = $null; P99Ms = $null; Errors = $null; Source = $null; JtlRequests = $null; JtlErrors = $null }
  switch ($tool) {
    'loadtool' {
      $r.Source = 'loadtool console summary'
      $text = Get-Content -Raw -Encoding UTF8 $stdout
      if (-not $text) { return $r }
      if ($text -match 'Requests:\s+([\d,]+) \(([\d.]+) req/s\)') {
        $r.Requests = [long]($Matches[1] -replace ',', ''); $r.RequestsPerSec = Get-Parsed $Matches[2]
      }
      if ($text -match 'Errors:\s+([\d,]+)') { $r.Errors = [long]($Matches[1] -replace ',', '') }
      foreach ($p in 'p50', 'p95', 'p99') {
        # \u00B5 is the micro sign; kept as an escape so this file stays ASCII.
        if ($text -match "$p\s+([\d.]+)(\u00B5s|ms|s)") { $r["$($p.ToUpper())Ms"] = ConvertTo-Ms $Matches[1] $Matches[2] }
      }
    }
    'k6' {
      $r.Source = 'k6 --summary-export'
      if (-not (Test-Path $cmd.Summary)) { return $r }
      $m = (Get-Content -Raw $cmd.Summary | ConvertFrom-Json).metrics
      $r.Requests = [long]$m.http_reqs.count; $r.RequestsPerSec = [double]$m.http_reqs.rate
      $r.Errors = [long]$m.http_req_failed.passes   # "passes" = requests that failed
      $r.P50Ms = [double]$m.http_req_duration.med
      $r.P95Ms = [double]$m.http_req_duration.'p(95)'; $r.P99Ms = [double]$m.http_req_duration.'p(99)'
    }
    'jmeter' {
      $r.Source = 'JMeter summariser (requests, rate, errors); percentiles calculated from JTL (nearest rank)'
      # The last "summary =" line is JMeter's cumulative total for the run.
      $line = Get-Content $stdout | Where-Object { $_ -match '^summary =' } | Select-Object -Last 1
      if ($line -match '^summary =\s+(\d+) in [\d:]+ =\s+([\d.]+)/s.*Err:\s+(\d+)') {
        $r.Requests = [long]$Matches[1]; $r.RequestsPerSec = Get-Parsed $Matches[2]; $r.Errors = [long]$Matches[3]
      }
      if (-not (Test-Path $cmd.Jtl) -or (Get-Item $cmd.Jtl).Length -eq 0) { return $r }
      $s = [JtlStats]::Read((Resolve-Path $cmd.Jtl).Path)
      $r.JtlRequests = $s.Requests; $r.JtlErrors = $s.Errors
      if ($s.Requests -gt 0) { $r.P50Ms = $s.P50; $r.P95Ms = $s.P95; $r.P99Ms = $s.P99 }
    }
  }
  return $r
}

function Invoke-Run([string] $tool, [int] $vus, [int] $seconds, [string] $kind, [int] $run) {
  $id = '{0}-vus{1:D4}-{2}{3}' -f $tool, $vus, $kind, $run
  $cmd = Get-Command-For $tool $vus $seconds $id
  # JMeter appends to an existing JTL, so start from an empty file.
  if ($cmd.Jtl -and (Test-Path $cmd.Jtl)) { Remove-Item $cmd.Jtl }
  $stdout = Join-Path $OutDir "$id-stdout.txt"
  $stderr = Join-Path $OutDir "$id-stderr.txt"
  $samplesFile = Join-Path $OutDir "$id-memory.csv"
  $serverCpuBefore = $script:serverProc.TotalProcessorTime.TotalSeconds
  $started = Get-Date

  $launcher = Start-Process -FilePath $cmd.Exe -ArgumentList $cmd.Args -PassThru -NoNewWindow `
    -RedirectStandardOutput $stdout -RedirectStandardError $stderr
  $null = $launcher.Handle
  $p = $launcher
  if ($cmd.Child) { $p = Find-Child $launcher.Id $cmd.Child }
  $null = $p.Handle  # keep the handle so times stay readable after exit

  $samples = New-Object System.Collections.Generic.List[string]
  $samples.Add('elapsed_s,private_mb,working_set_mb')
  $peakPrivate = 0; $peakWS = 0; $sumPrivate = 0; $sumWS = 0; $n = 0
  while (-not $p.HasExited) {
    try {
      $p.Refresh()
      $priv = $p.PrivateMemorySize64; $ws = $p.WorkingSet64
      $peakPrivate = [Math]::Max($peakPrivate, $priv); $peakWS = [Math]::Max($peakWS, $ws)
      $sumPrivate += $priv; $sumWS += $ws; $n++
      $samples.Add([string]::Format($inv, '{0:F2},{1:F1},{2:F1}', ((Get-Date) - $started).TotalSeconds, ($priv / 1MB), ($ws / 1MB)))
    } catch {}
    Start-Sleep -Milliseconds 250
  }
  $p.WaitForExit(); $launcher.WaitForExit()
  [IO.File]::WriteAllLines((Join-Path (Get-Location) $samplesFile), $samples)

  $wall = ($p.ExitTime - $p.StartTime).TotalSeconds
  $cpuSec = $p.TotalProcessorTime.TotalSeconds
  $serverCpu = $script:serverProc.TotalProcessorTime.TotalSeconds - $serverCpuBefore
  $serverWall = ((Get-Date) - $started).TotalSeconds
  $r = Read-Result $tool $cmd $stdout

  $errorRate = $null
  if ($r.Requests -gt 0 -and $null -ne $r.Errors) { $errorRate = 100.0 * $r.Errors / $r.Requests }
  $avgPrivate = $null; $avgWS = $null
  if ($n -gt 0) {
    $avgPrivate = [Math]::Round($sumPrivate / $n / 1MB, 1); $avgWS = [Math]::Round($sumWS / $n / 1MB, 1)
  }
  $jtlInfo = $null
  if ($cmd.Jtl -and (Test-Path $cmd.Jtl)) {
    $jtlInfo = @{ path = $cmd.Jtl; bytes = (Get-Item $cmd.Jtl).Length; sha256 = (Get-FileHash -Algorithm SHA256 $cmd.Jtl).Hash }
  }

  $rec = [ordered]@{
    tool = $tool; vus = $vus; kind = $kind; run = $run; id = $id
    startedAt = $started.ToString('o'); durationSec = $seconds; exitCode = $launcher.ExitCode
    # measured
    processWallSec = [Math]::Round($wall, 2); processCpuSec = [Math]::Round($cpuSec, 2)
    peakPrivateMB = [Math]::Round($peakPrivate / 1MB, 1); peakWorkingSetMB = [Math]::Round($peakWS / 1MB, 1)
    memorySamples = $n
    requests = $r.Requests; errors = $r.Errors
    requestsPerSec = $r.RequestsPerSec; p50Ms = $r.P50Ms; p95Ms = $r.P95Ms; p99Ms = $r.P99Ms
    resultSource = $r.Source
    # calculated
    cpuPercent = [Math]::Round($cpuSec / $wall / $logicalCPUs * 100, 2)
    cpuCores = [Math]::Round($cpuSec / $wall, 2)
    avgPrivateMB = $avgPrivate
    avgWorkingSetMB = $avgWS
    errorRatePct = $errorRate
    serverCpuPercent = [Math]::Round($serverCpu / $serverWall / $logicalCPUs * 100, 2)
    jtl = $jtlInfo
    jtlRequests = $r.JtlRequests; jtlErrors = $r.JtlErrors   # cross-check of JMeter's own counts
  }
  Write-Utf8 $runsFile (($rec | ConvertTo-Json -Compress -Depth 4) + "`n") -Append
  return [pscustomobject]$rec
}

# --- run ---------------------------------------------------------------------
if (Test-Path $runsFile) { throw "$runsFile already exists; pass a new -OutDir so results from different sessions are not mixed" }
$environment = Get-Environment
Write-Utf8 (Join-Path $OutDir 'environment.json') ($environment | ConvertTo-Json -Depth 4)
Write-Host ($environment | Out-String)

$script:serverProc = Start-Process -FilePath $Server -ArgumentList "-addr $ServerAddr -delay $ServerDelay" `
  -PassThru -NoNewWindow -RedirectStandardOutput (Join-Path $OutDir 'server-stdout.txt') `
  -RedirectStandardError (Join-Path $OutDir 'server-stderr.txt')
$null = $script:serverProc.Handle
try {
  $healthy = $false
  for ($i = 0; $i -lt 50 -and -not $healthy; $i++) {
    try { $null = Invoke-WebRequest -UseBasicParsing -TimeoutSec 2 "http://$ServerAddr/health"; $healthy = $true }
    catch { Start-Sleep -Milliseconds 200 }
  }
  if (-not $healthy) { throw "benchmark server did not become healthy at http://$ServerAddr/health" }

  $results = @()
  foreach ($v in $VUs) {
    if ($WarmupSec -gt 0) {
      foreach ($tool in $Tools) {
        Write-Host ("[{0:HH:mm:ss}] warm-up  {1,-8} {2,4} VUs {3}s" -f (Get-Date), $tool, $v, $WarmupSec)
        $null = Invoke-Run $tool $v $WarmupSec 'warmup' 1
        Start-Sleep -Seconds $CooldownSec
      }
    }
    foreach ($i in 1..$Runs) {
      foreach ($tool in $Tools) {
        Write-Host ("[{0:HH:mm:ss}] run {1}/{2}  {3,-8} {4,4} VUs {5}s" -f (Get-Date), $i, $Runs, $tool, $v, $DurationSec)
        $r = Invoke-Run $tool $v $DurationSec 'run' $i
        if ($r.exitCode -ne 0) { Write-Warning "$tool exited with code $($r.exitCode); see $OutDir\$($r.id)-stderr.txt" }
        $results += $r
        Start-Sleep -Seconds $CooldownSec
      }
    }
  }
} finally {
  Stop-Process -Id $script:serverProc.Id -Confirm:$false -ErrorAction SilentlyContinue
}

$results | Format-Table tool, vus, run, exitCode, cpuPercent, peakPrivateMB, avgPrivateMB, requestsPerSec, p50Ms, p95Ms, p99Ms, errors, errorRatePct -AutoSize |
  Out-String -Width 220 | Write-Host
Write-Host "Raw results: $runsFile"

} finally {
  Pop-Location
}
