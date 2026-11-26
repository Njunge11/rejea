"""Run 5: give the best 50 passages of a saved run to the Cohere reranker, with the same request as Rejea, and save the new order.
Use: rerank_run.py <questions.json> <corpus.json> <run in> <run out> <pause seconds>. The key comes from the variable COHERE_KEY."""
import json, os, sys, time, urllib.request, urllib.error
qs_path, corpus_path, in_path, out_path, pause = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], float(sys.argv[5])
corpus = json.load(open(corpus_path)); questions = {q["n"]: q for q in json.load(open(qs_path))}
runs = json.load(open(in_path))
try: done = {r["n"]: r for r in json.load(open(out_path))}
except FileNotFoundError: done = {}
def rerank(query, texts):
    body = json.dumps({"model": "rerank-v5.0-fast", "query": query, "documents": texts, "max_tokens_per_doc": 8192}).encode()
    for attempt in range(6):
        try:
            req = urllib.request.Request("https://api.cohere.com/v2/rerank", body, {"content-type": "application/json", "authorization": "Bearer " + os.environ["COHERE_KEY"]})
            res = json.load(urllib.request.urlopen(req, timeout=120))["results"]
            return [r["index"] for r in sorted(res, key=lambda r: -r["relevance_score"])]
        except urllib.error.HTTPError as e:
            text = e.read().decode()[:200]
            if e.code != 429 or attempt == 5: sys.exit(f"STOP: Cohere answers HTTP {e.code}: {text}")
            print(f"  HTTP 429, wait 65 s: {text[:100]}", flush=True); time.sleep(65)
for i, r in enumerate(runs, 1):
    if r["n"] in done: continue
    top = r["results"][:50]
    texts = [corpus[n]["title"] + "\n\n" + corpus[n]["text"] for n in top]      # the text of a Rejea chunk: the title, an empty line, the passage
    order = rerank(questions[r["n"]]["question"], texts)
    done[r["n"]] = {"n": r["n"], "results": [top[j] for j in order]}
    json.dump([done[k] for k in sorted(done)], open(out_path, "w"))
    if i % 25 == 0: print(f"  {i} of {len(runs)}", flush=True)
    time.sleep(pause)
print("saved", len(done), "questions to", out_path)
