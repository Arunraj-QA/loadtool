# Before/after A/B for Phase 1 step 4 (see README.md in this folder).
# Usage: ./ab.ps1 -Before <loadtool at bbbb685> -After <loadtool at step 4> -Server <benchserver>
param(
  [Parameter(Mandatory)] [string]$Before,
  [Parameter(Mandatory)] [string]$After,
  [Parameter(Mandatory)] [string]$Server,
  [int]$VUs = 1000, [int]$Sec = 20, [int]$Rounds = 3, [string[]]$Scenarios = @('http', 'cpu'))
$ErrorActionPreference = 'Stop'
$sp = $PSScriptRoot
$srv = Start-Process $Server -ArgumentList '-addr','127.0.0.1:8080','-delay','10ms' -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 2
$variants = @(
  @{ name = 'before'; exe = $Before; http = "$sp\http-before.ts" },
  @{ name = 'after';  exe = $After;  http = "$sp\http-after.ts" }
)
"script,variant,round,peakWorkingSetMB,peakPrivateMB,requests,reqPerSec,errors,exit"
try {
  foreach ($scen in $Scenarios) {
    for ($r = 1; $r -le $Rounds; $r++) {
      $order = if ($r % 2) { $variants } else { @($variants[1], $variants[0]) }
      foreach ($v in $order) {
        $script = if ($scen -eq 'http') { $v.http } else { "$sp\cpu.ts" }
        $out = Join-Path $env:TEMP 'ab-out.txt'
        $p = Start-Process $v.exe -ArgumentList 'run', "`"$script`"", '--vus', $VUs, '--duration', "${Sec}s" -PassThru -WindowStyle Hidden -RedirectStandardOutput $out -RedirectStandardError (Join-Path $env:TEMP 'ab-err.txt')
        $null = $p.Handle  # keeps the exit code available
        $ws = 0; $pv = 0
        while (-not $p.HasExited) {
          try { $p.Refresh(); $ws = [Math]::Max($ws, $p.WorkingSet64); $pv = [Math]::Max($pv, $p.PrivateMemorySize64) } catch {}
          Start-Sleep -Milliseconds 250
        }
        $p.WaitForExit()
        $txt = Get-Content $out -Raw
        $req = if ($txt -cmatch 'Requests:\s+([\d,]+)') { $Matches[1] -replace ',', '' } else { '' }
        $err = if ($txt -cmatch 'Errors:\s+([\d,]+)') { $Matches[1] -replace ',', '' } else { '' }
        $rps = if ($txt -cmatch 'Requests:\s+[\d,]+ \(([\d,.]+) req/s\)') { $Matches[1] -replace ',', '' } else { '' }
        '{0},{1},{2},{3:F1},{4:F1},{5},{6},{7},{8}' -f $scen, $v.name, $r, ($ws/1MB), ($pv/1MB), $req, $rps, $err, $p.ExitCode
        Start-Sleep -Seconds 3
      }
    }
  }
} finally { Stop-Process -Id $srv.Id -Force }
