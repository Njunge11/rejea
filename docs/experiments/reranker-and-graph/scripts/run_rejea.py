"""Send each test question to a Rejea service and save the passage numbers of the results, in order.
Use: run_rejea.py <questions.json> <out.json> <rerank: true|false> <limit> [pause seconds]"""
import json, re, sys, time, urllib.request, urllib.error
qs_path, out_path, rerank, limit = sys.argv[1], sys.argv[2], sys.argv[3] == "true", int(sys.argv[4])
pause = float(sys.argv[5]) if len(sys.argv) > 5 else 0
URL = "http://localhost:8081/search"
def search(q):
    body = json.dumps({"query": q, "limit": limit, "rerank": rerank}).encode()
    for attempt in range(6):
        try:
            req = urllib.request.Request(URL, body, {"content-type": "application/json"})
            return json.load(urllib.request.urlopen(req, timeout=120))["results"]
        except urllib.error.HTTPError as e:
            text = e.read().decode()[:300]
            refused_query = e.code == 400 and "refuses the query" in text   # a false answer of the provider when requests run at the same time
            if not (e.code == 503 or refused_query) or attempt == 5: sys.exit(f"STOP at a search: HTTP {e.code}: {text}")
            wait = 3 if refused_query else 65
            print(f"  HTTP {e.code}, wait {wait} s (attempt {attempt + 1}): {text[:120]}", flush=True); time.sleep(wait)
questions = json.load(open(qs_path))
try: done = {r["n"]: r for r in json.load(open(out_path))}
except FileNotFoundError: done = {}
for i, q in enumerate(questions, 1):
    if q["n"] in done: continue
    res = search(q["question"])
    done[q["n"]] = {"n": q["n"], "results": [int(re.search(r"-(\d+)\.docx$", r["document"]["filename"]).group(1)) for r in res]}
    json.dump([done[k] for k in sorted(done)], open(out_path, "w"))
    if i % 25 == 0: print(f"  {i} of {len(questions)}", flush=True)
    if pause: time.sleep(pause)
print("saved", len(done), "questions to", out_path)
