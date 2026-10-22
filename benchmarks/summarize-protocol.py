"""Summarize benchmarks/protocol.ps1 runs: medians (with ranges) per level.

    python benchmarks/summarize-protocol.py <OutDir>/runs.jsonl [--bytes N]

Warm-ups are left out. For each level it prints LoadTool's and the
server's peak memory and CPU, script errors, and each recorded metric:
counters as total and rate, trends as p50/p95/p99/max, rates as the
share of trues. With --bytes, a "kafka_messages_*" counter's rate is also
given in MB/s (rate x bytes / 2^20).
"""
import json
import statistics
import sys


def fmt(xs, digits=1):
    xs = [x for x in xs if x is not None]
    if not xs:
        return "-"
    m = statistics.median(xs)
    if min(xs) == max(xs):
        return f"{m:,.{digits}f}"
    return f"{m:,.{digits}f} ({min(xs):,.{digits}f}-{max(xs):,.{digits}f})"


def main():
    path = sys.argv[1]
    size = None
    if "--bytes" in sys.argv:
        size = int(sys.argv[sys.argv.index("--bytes") + 1])
    with open(path, encoding="utf-8-sig") as f:
        rows = [json.loads(line) for line in f if line.strip()]
    runs = [r for r in rows if r["round"] != "warmup"]
    for level in sorted({r["level"] for r in runs}):
        rs = [r for r in runs if r["level"] == level]
        print(f"== {rs[0]['levelKind']} {level}: {len(rs)} runs, exit codes {[r['exitCode'] for r in rs]}, script errors {[r['scriptErrors'] for r in rs]}")
        print(f"   LoadTool peak private MB {fmt([r['peakPrivateMB'] for r in rs])}, CPU % {fmt([r['cpuPercentOfMachine'] for r in rs])}")
        print(f"   server peak private MB {fmt([r['serverPeakPrivateMB'] for r in rs])}, CPU % {fmt([r['serverCpuPercentOfMachine'] for r in rs])}")
        names = []
        for r in rs:
            for n in r["metrics"]:
                if n not in names:
                    names.append(n)
        for n in names:
            ms = [r["metrics"].get(n) for r in rs]
            ms = [m for m in ms if m]
            kind = ms[0].get("kind")
            if "p95" in ms[0]:
                print(f"   {n}: p50 {fmt([m['p50'] for m in ms], 2)} p95 {fmt([m['p95'] for m in ms], 2)} p99 {fmt([m['p99'] for m in ms], 2)} max {fmt([m['max'] for m in ms], 2)} ms; samples {fmt([m.get('count') for m in ms], 0)}")
            elif kind == "rate" or "trues" in ms[0]:
                print(f"   {n}: {fmt([100 * m['trues'] / m['count'] if m['count'] else 0 for m in ms], 3)} % ({fmt([m['trues'] for m in ms], 0)} of {fmt([m['count'] for m in ms], 0)})")
            else:
                line = f"   {n}: total {fmt([m['count'] for m in ms], 0)}, per second {fmt([m['rate'] for m in ms], 1)}"
                if size and n.startswith("kafka_messages_"):
                    line += f", MB/s {fmt([m['rate'] * size / 2**20 for m in ms], 2)}"
                print(line)


if __name__ == "__main__":
    main()
