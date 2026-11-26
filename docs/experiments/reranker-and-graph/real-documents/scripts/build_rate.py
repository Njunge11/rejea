"""Reads the time log of a graph build and gives the steady rate of each step.
Use: analyze.py <time log .jsonl> <HTTP log> <build log> <chunks> <window seconds>
A rate is steady by the rule of NVIDIA's Perf Analyzer: max / min of 3 windows in sequence is within 10%."""
import json, sys, statistics as st
time_log, http_log, build_log, chunks, window = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
rows = [json.loads(l) for l in open(time_log)]
chat = sorted((r for r in rows if r["kind"] == "chat"), key=lambda r: r["end"]); vec = sorted((r for r in rows if r["kind"] == "vector"), key=lambda r: r["end"])
# The build reads the names of all chunks first, then the facts of all chunks: the first calls to start are the name calls.
by_start = sorted(chat, key=lambda r: r["start"]); names, facts = by_start[:chunks], by_start[chunks:]

def windows(part, value):
    """The sum of value for each full window between the first start and the last end."""
    t0, t1 = min(r["start"] for r in part), max(r["end"] for r in part); n = int((t1 - t0) // window); sums = [0.0] * n
    for r in part:
        i = int((r["end"] - t0) // window)
        if i < n: sums[i] += value(r)
    return sums, t1 - t0

def steady(sums):
    """The mean of the longest run of windows where each 3 in sequence are within 10%."""
    best = []
    for i in range(len(sums) - 2):
        j = i + 3
        if min(sums[i:j]) <= 0 or max(sums[i:j]) / min(sums[i:j]) > 1.10: continue
        while j < len(sums) and min(sums[j - 2:j + 1]) > 0 and max(sums[j - 2:j + 1]) / min(sums[j - 2:j + 1]) <= 1.10: j += 1
        if j - i > len(best): best = sums[i:j]
    return (st.mean(best), len(best)) if best else (None, 0)

def report(name, part, per_chunk_calls):
    if not part: return
    dur = [r["end"] - r["start"] for r in part]; calls, seconds = windows(part, lambda r: 1); tokens, _ = windows(part, lambda r: r["out"])
    print(f"{name}: {len(part)} calls in {seconds:.0f} s | one call: median {st.median(dur):.1f} s, mean {st.mean(dur):.1f} s, slowest {max(dur):.1f} s | output tokens of one call: mean {st.mean(r['out'] for r in part):.0f}")
    per_min = [c * 60 / window for c in calls]; tok_min = [t * 60 / window for t in tokens]
    print(f"  calls a minute in each window of {window} s: {[round(x) for x in per_min]}")
    print(f"  output tokens a minute in each window: {[round(x / 1000) for x in tok_min]} thousand (the limit of the account is 400 thousand)")
    rate, n = steady(per_min)
    print(f"  steady rate: {rate:.0f} calls a minute over {n} windows = {rate / per_chunk_calls:.0f} chunks a minute" if rate else f"  steady rate: none (no 3 windows in sequence within 10%) | mean of all: {len(part) / seconds * 60:.0f} calls a minute")
    return len(part) / seconds * 60

report("names step (chat)", names, 1); report("facts step (chat)", facts, 1)
t0, t1 = min(r["start"] for r in chat), max(r["end"] for r in chat)
print(f"chat step as a whole: {chunks} chunks in {t1 - t0:.0f} s = {chunks / (t1 - t0) * 60:.0f} chunks a minute | output tokens {sum(r['out'] for r in chat)} = {sum(r['out'] for r in chat) / (t1 - t0) * 60 / 1000:.0f} thousand a minute")
if vec:
    v0, v1 = min(r["start"] for r in vec), max(r["end"] for r in vec); texts = sum(r["out"] for r in vec); dur = [r["end"] - r["start"] for r in vec]
    reqs, _ = windows(vec, lambda r: 1)
    print(f"vector step: {len(vec)} requests, {texts} texts ({texts / chunks:.1f} for a chunk) in {v1 - v0:.0f} s (first start to last end) = {len(vec) / (v1 - v0) * 60:.0f} requests a minute | one request: median {st.median(dur):.1f} s, slowest {max(dur):.1f} s")
    print(f"  requests a minute in each window of {window} s: {[round(c * 60 / window) for c in reqs]}")
lines = open(http_log, errors="replace").read().splitlines()
for host in ("api.anthropic.com", "api.cloudflare.com"):
    mine = [l for l in lines if host in l]; bad = [l for l in mine if '"HTTP/1.1 2' not in l and "HTTP/2 2" not in l]
    print(f"{host}: {len(mine)} HTTP requests | not a success: {len(bad)} | status 429: {sum(1 for l in bad if ' 429 ' in l)}")
for l in open(build_log, errors="replace"):
    if l.startswith('{"phase"'):
        d = json.loads(l); print({k: d[k] for k in ("chunks", "seconds", "chat seconds", "vector seconds", "chat calls", "input tokens", "output tokens", "chat cost usd", "vector requests", "vector texts", "chat workers", "vector workers")})
    if l.startswith(("graph:", "answers not read", "STOP")): print(l.strip()[:300])
