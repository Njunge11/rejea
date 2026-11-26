"""Score saved runs against the named passages, and compare two runs on the same questions.
Use: score.py <questions.json> <k> <name=run.json> ... [--compare a b] ..."""
import json, random, sys
args = sys.argv[1:]; qs = {q["n"]: q for q in json.load(open(args[0]))}; k = int(args[1])
runs, compares, i = {}, [], 2
while i < len(args):
    if args[i] == "--compare": compares.append((args[i + 1], args[i + 2])); i += 3
    else: name, path = args[i].split("=", 1); runs[name] = {r["n"]: r["results"] for r in json.load(open(path))}; i += 1
def per_question(run):
    rec, full = {}, {}
    for n, q in qs.items():
        top = set(run[n][:k]); hit = sum(1 for g in q["gold"] if g in top)
        rec[n] = hit / len(q["gold"]); full[n] = 1.0 if hit == len(q["gold"]) else 0.0
    return rec, full
scores = {name: per_question(run) for name, run in runs.items()}
order = sorted(qs)
print(f"questions: {len(order)} | k = {k}")
for name, (rec, full) in scores.items():
    print(f"  {name:<28} recall at {k}: {100 * sum(rec.values()) / len(rec):5.1f}%   all found at {k}: {100 * sum(full.values()) / len(full):5.1f}%")
rng = random.Random(7)
def paired(a, b):
    d = [b[n] - a[n] for n in order]; mean = sum(d) / len(d)
    more = 0
    for _ in range(10000):
        s = sum(x if rng.random() < 0.5 else -x for x in d) / len(d)
        if abs(s) >= abs(mean) - 1e-12: more += 1
    boots = sorted(sum(rng.choice(d) for _ in d) / len(d) for _ in range(10000))
    return mean, more / 10000, boots[249], boots[9749], sum(1 for x in d if x > 0), sum(1 for x in d if x < 0)
for a, b in compares:
    for label, idx in (("recall", 0), ("all found", 1)):
        mean, p, lo, hi, up, down = paired(scores[a][idx], scores[b][idx])
        print(f"  {b} minus {a} ({label}): {100 * mean:+.1f} points | 95% interval {100 * lo:+.1f} to {100 * hi:+.1f} | p = {p:.4f} | better on {up} questions, worse on {down}")
