# Measures peak process memory of one `loadtool run`.
#
# Usage (from the repository root):
#   go build -o bin/loadtool.exe ./cmd/loadtool
#   ./benchmarks/tools/peak-memory.ps1 -Script examples/basic.ts -VUs 1000 -Duration 10s
#
# Samples every 100 ms; peaks shorter than that may be missed.
param(
  [Parameter(Mandatory)] [string] $Script,
  [int] $VUs = 1,
  [string] $Duration = "10s",
  [string] $Binary = "bin/loadtool.exe"
)

$out = New-TemporaryFile
$p = Start-Process -FilePath $Binary -ArgumentList "run `"$Script`" --vus $VUs --duration $Duration" `
  -PassThru -NoNewWindow -RedirectStandardOutput $out
$peakWS = 0; $peakPrivate = 0
while (-not $p.HasExited) {
  try {
    $p.Refresh()
    $peakWS = [Math]::Max($peakWS, $p.PeakWorkingSet64)
    $peakPrivate = [Math]::Max($peakPrivate, $p.PrivateMemorySize64)
  } catch {}
  Start-Sleep -Milliseconds 100
}
$requests = (Select-String -Path $out -Pattern "Requests:").Line.Trim()
Remove-Item $out
"{0} VUs: peak working set {1:N1} MB, peak private {2:N1} MB | {3}" -f $VUs, ($peakWS / 1MB), ($peakPrivate / 1MB), $requests
