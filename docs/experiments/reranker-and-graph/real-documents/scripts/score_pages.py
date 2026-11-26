"""Score saved runs by page. A marked answer page counts as found when one of the first k chunks is of that document and covers that page.
The score of a question is the share of its marked pages that are found. Use: score_pages.py <questions.json> <k> <name=run.json> ... [--compare a b] ..."""
import json, random, sys, collections
args = sys.argv[1:]; qs = {q["n"]: q for q in json.load(open(args[0]))}; k = int(args[1])
runs, compares, i = {}, [], 2
while i < len(args):
    if args[i] == "--compare": compares.append((args[i + 1], args[i + 2])); i += 3
    else:
        # A result is {"doc", "page_start", "page_end"}, or the short form of the saved runs: [doc, page_start, page_end].
        name, path = args[i].split("=", 1); i += 1
        runs[name] = {r["n"]: [c if isinstance(c, dict) else {"doc": c[0], "page_start": c[1], "page_end": c[2]} for c in r["results"]] for r in json.load(open(path))}
def found(gold, top):
    doc, page = gold[0], gold[1]
    return any(c["doc"] == doc and c["page_start"] is not None and c["page_start"] <= page <= (c["page_end"] or c["page_start"]) for c in top)
def per_question(run):
    missing = [n for n in qs if n not in run]
    if missing: sys.exit(f"STOP: the run has no result for {len(missing)} questions: {missing[:5]}")
    return {n: sum(found(g, run[n][:k]) for g in q["gold"]) / len(q["gold"]) for n, q in qs.items()}
scores = {name: per_question(run) for name, run in runs.items()}; order = sorted(qs)
kinds = sorted({q["answer_type"] for q in qs.values()})
print(f"questions: {len(order)} | first {k} chunks | by answer kind: " + ", ".join(f"{a} {sum(1 for q in qs.values() if q['answer_type'] == a)}" for a in kinds))
for name, s in scores.items():
    parts = " | ".join(f"{a}: {100 * sum(s[n] for n in order if qs[n]['answer_type'] == a) / max(1, sum(1 for n in order if qs[n]['answer_type'] == a)):.0f}%" for a in kinds)
    print(f"  {name:<24} {100 * sum(s.values()) / len(s):5.1f}%   {parts}")
rng = random.Random(7)
for a, b in compares:
    d = [scores[b][n] - scores[a][n] for n in order]; mean = sum(d) / len(d)
    boots = sorted(sum(rng.choice(d) for _ in d) / len(d) for _ in range(10000))
    print(f"  {b} minus {a}: {100 * mean:+.1f} points | 95% range {100 * boots[250]:+.1f} to {100 * boots[9750]:+.1f} | up for {sum(1 for x in d if x > 0)} questions, down for {sum(1 for x in d if x < 0)}")
