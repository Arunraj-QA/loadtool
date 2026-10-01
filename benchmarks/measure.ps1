# Measures LoadTool and k6 against the benchmark server, following the
# methodology in benchmarks/README.md: one discarded warm-up run per tool,
# then measured runs that alternate between tools.
#
# For every run it reports, for the tool's own process:
#   CPU %      CPU time / wall time / logical CPUs (share of the whole machine)
#   Memory MB  peak private bytes, sampled every 250 ms
# and, from the tool's own summary: requests/sec, p50, p95, p99, errors.
#
# Usage (from anywhere; paths are relative to the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   ./benchmarks/measure.ps1 -VUs 100 -DurationSec 60 -Runs 3
#
# Start the server first: go run ./benchmarks/server
param(
  [ValidateSet('loadtool', 'k6', 'jmeter')]
  [string[]] $Tools = @('loadtool', 'k6'),
  [int] $VUs = 100,
  [int] $DurationSec = 60,
  [int] $Runs = 3,
  [int] $WarmupSec = 30,
  [string] $Target = 'http://127.0.0.1:8080/api/test',
  [string] $LoadTool = 'bin/loadtool.exe',
  # The real k6 executable. A Chocolatey launcher would hide the process
  # that does the work, so the real k6.exe is looked up when this is empty.
  [string] $K6 = '',
  [string] $OutDir = 'bench-out'
)

$ErrorActionPreference = 'Stop'
Push-Location (Split-Path $PSScriptRoot -Parent)
try {
$logicalCPUs = (Get-CimInstance Win32_Processor | Measure-Object NumberOfLogicalProcessors -Sum).Sum
New-Item -ItemType Directory -Force $OutDir | Out-Null

function Resolve-K6 {
  if ($K6) { return $K6 }
  $real = Get-ChildItem 'C:\ProgramData\chocolatey\lib\k6\tools\*\k6.exe' -ErrorAction SilentlyContinue |
    Select-Object -First 1
  if ($real) { return $real.FullName }
  return (Get-Command k6).Source
}

function Assert-ServerUp {
  $u = [uri]$Target
  $health = '{0}://{1}:{2}/health' -f $u.Scheme, $u.Host, $u.Port
  try { $null = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 $health }
  catch { throw "benchmark server is not reachable at $health; start it with: go run ./benchmarks/server" }
}

# Converts "10.72ms" / "450.00us" (micro sign) / "1.20s" from the LoadTool summary to ms.
function ConvertTo-Ms([string] $value, [string] $unit) {
  $v = [double]::Parse($value, [Globalization.CultureInfo]::InvariantCulture)
  switch ($unit) { 's' { return $v * 1000 } 'ms' { return $v } default { return $v / 1000 } }
}

function Get-LoadToolCommand([int] $seconds, [string] $id) {
  # Same scenario, with TARGET substituted so -Target works like k6's -e.
  $scenario = Join-Path $OutDir "$id-scenario.ts"
  (Get-Content -Raw -Encoding UTF8 'benchmarks/loadtool/scenario.ts') -replace
    'const TARGET = "[^"]*";', "const TARGET = `"$Target`";" |
    Set-Content -Encoding UTF8 $scenario
  return @{ Exe = $LoadTool; Args = "run $scenario --vus $VUs --duration ${seconds}s" }
}

function Get-K6Command([int] $seconds, [string] $id) {
  $json = Join-Path $OutDir "$id-summary.json"
  $a = "run --quiet --vus $VUs --duration ${seconds}s --summary-trend-stats med,p(95),p(99) " +
       "--summary-export $json -e TARGET=$Target benchmarks/k6/scenario.js"
  return @{ Exe = (Resolve-K6); Args = $a; Summary = $json }
}

function Read-LoadToolResult([string] $stdout) {
  $text = Get-Content -Raw -Encoding UTF8 $stdout
  $r = @{}
  if ($text -match 'Requests:\s+([\d,]+) \(([\d.]+) req/s\)') {
    $r.Requests = [int]($Matches[1] -replace ',', '')
    $r.RequestsPerSec = [double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture)
  }
  if ($text -match 'Errors:\s+([\d,]+)') { $r.Errors = [int]($Matches[1] -replace ',', '') }
  foreach ($p in 'p50', 'p95', 'p99') {
    # µ is the micro sign; kept as an escape so this file stays ASCII.
    if ($text -match "$p\s+([\d.]+)(µs|ms|s)") { $r["${p}Ms"] = ConvertTo-Ms $Matches[1] $Matches[2] }
  }
  return $r
}

function Read-K6Result([string] $summary) {
  $m = (Get-Content -Raw $summary | ConvertFrom-Json).metrics
  return @{
    Requests       = [int]$m.http_reqs.count
    RequestsPerSec = [double]$m.http_reqs.rate
    Errors         = [int]$m.http_req_failed.passes   # "passes" = requests that failed
    p50Ms          = [double]$m.http_req_duration.med
    p95Ms          = [double]$m.http_req_duration.'p(95)'
    p99Ms          = [double]$m.http_req_duration.'p(99)'
  }
}

function Invoke-Measured([string] $tool, [int] $seconds, [string] $id) {
  switch ($tool) {
    'loadtool' { $cmd = Get-LoadToolCommand $seconds $id }
    'k6'       { $cmd = Get-K6Command $seconds $id }
    'jmeter'   { throw 'JMeter is not supported by this script yet: it has not been installed and verified.' }
  }
  $stdout = Join-Path $OutDir "$id-stdout.txt"
  $stderr = Join-Path $OutDir "$id-stderr.txt"

  $start = Get-Date
  $p = Start-Process -FilePath $cmd.Exe -ArgumentList $cmd.Args -PassThru -NoNewWindow `
    -RedirectStandardOutput $stdout -RedirectStandardError $stderr
  $null = $p.Handle  # keep the handle so CPU time is readable after exit
  $peakPrivate = 0
  while (-not $p.HasExited) {
    try { $p.Refresh(); $peakPrivate = [Math]::Max($peakPrivate, $p.PrivateMemorySize64) } catch {}
    Start-Sleep -Milliseconds 250
  }
  $p.WaitForExit()
  $wall = ((Get-Date) - $start).TotalSeconds
  $cpuSec = $p.TotalProcessorTime.TotalSeconds

  if ($tool -eq 'k6') { $r = Read-K6Result $cmd.Summary } else { $r = Read-LoadToolResult $stdout }
  return [pscustomobject]@{
    Tool           = $tool
    Run            = $id
    VUs            = $VUs
    DurationSec    = $seconds
    ExitCode       = $p.ExitCode
    WallSec        = [Math]::Round($wall, 2)
    CPUPercent     = [Math]::Round($cpuSec / $wall / $logicalCPUs * 100, 1)
    CPUCores       = [Math]::Round($cpuSec / $wall, 2)
    MemoryMB       = [Math]::Round($peakPrivate / 1MB, 1)
    Requests       = $r.Requests
    RequestsPerSec = [Math]::Round($r.RequestsPerSec, 1)
    P50Ms          = [Math]::Round($r.p50Ms, 2)
    P95Ms          = [Math]::Round($r.p95Ms, 2)
    P99Ms          = [Math]::Round($r.p99Ms, 2)
    Errors         = $r.Errors
  }
}

function Get-Median([double[]] $v) {
  $s = $v | Sort-Object
  $n = $s.Count
  if ($n % 2) { return $s[($n - 1) / 2] }
  return ($s[$n / 2 - 1] + $s[$n / 2]) / 2
}

Assert-ServerUp
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$runsFile = Join-Path $OutDir "$stamp-runs.jsonl"
Write-Host "Target $Target | VUs $VUs | ${DurationSec}s x $Runs runs | warm-up ${WarmupSec}s | $logicalCPUs logical CPUs"

if ($WarmupSec -gt 0) {
  foreach ($tool in $Tools) {
    Write-Host "warm-up: $tool"
    $null = Invoke-Measured $tool $WarmupSec "$stamp-warmup-$tool"
  }
}

$results = @()
foreach ($i in 1..$Runs) {
  foreach ($tool in $Tools) {
    Write-Host "run $i/${Runs}: $tool"
    $r = Invoke-Measured $tool $DurationSec "$stamp-run$i-$tool"
    $r | ConvertTo-Json -Compress | Add-Content -Encoding UTF8 $runsFile
    if ($r.ExitCode -ne 0) { Write-Warning "$tool exited with code $($r.ExitCode); see $OutDir" }
    $results += $r
  }
}

Write-Host "`nAll runs (also in $runsFile):"
$results | Format-Table Tool, Run, CPUPercent, CPUCores, MemoryMB, RequestsPerSec, P50Ms, P95Ms, P99Ms, Errors -AutoSize | Out-String | Write-Host

Write-Host 'Median (min-max) per tool:'
foreach ($tool in $Tools) {
  $t = $results | Where-Object Tool -eq $tool
  $row = [ordered]@{ Tool = $tool }
  foreach ($f in 'CPUPercent', 'MemoryMB', 'RequestsPerSec', 'P50Ms', 'P95Ms', 'P99Ms', 'Errors') {
    $v = [double[]]($t | ForEach-Object { $_.$f })
    $row[$f] = '{0} ({1}-{2})' -f (Get-Median $v), ($v | Measure-Object -Minimum).Minimum, ($v | Measure-Object -Maximum).Maximum
  }
  [pscustomobject]$row | Format-List | Out-String | Write-Host
}
} finally {
  Pop-Location
}
