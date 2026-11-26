"""Give the best 50 chunks of a saved run to the reranker on Amazon Bedrock, with the same request as Rejea (internal/bedrock), and save the new order.
Use: rerank_run.py <questions.json> <chunks.json> <run in> <run out> <requests at the same time>
Settings: BEDROCK_KEY (an Amazon Bedrock API key), BEDROCK_URL (default: us-east-1), RERANK_MODEL (default cohere.rerank-v3-5:0)."""
import json, os, sys, time, urllib.request, urllib.error
from concurrent.futures import ThreadPoolExecutor
qs_path, chunks_path, in_path, out_path, workers = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5])
text = {c["chunk"]: c["text"] for c in json.load(open(chunks_path))}; questions = {q["n"]: q for q in json.load(open(qs_path))}
runs = json.load(open(in_path)); model = os.environ.get("RERANK_MODEL", "cohere.rerank-v3-5:0")
address = os.environ.get("BEDROCK_URL", "https://bedrock-runtime.us-east-1.amazonaws.com") + "/model/" + model + "/invoke"
def rerank(query, texts):
    body = json.dumps({"api_version": 2, "query": query, "documents": texts}).encode()
    for attempt in range(6):
        try:
            req = urllib.request.Request(address, body, {"content-type": "application/json", "authorization": "Bearer " + os.environ["BEDROCK_KEY"]})
            res = json.load(urllib.request.urlopen(req, timeout=120))["results"]
            return [r["index"] for r in sorted(res, key=lambda r: -r["relevance_score"])]
        except urllib.error.HTTPError as e:
            msg = e.read().decode()[:200]
            # Status 429 and a status from 500 can succeed on a new attempt: wait, then send the request again.
            if (e.code != 429 and e.code < 500) or attempt == 5: raise RuntimeError(f"Amazon Bedrock answers HTTP {e.code}: {msg}")
            time.sleep(5 * (attempt + 1))
def one(r):
    top = r["results"][:50]; order = rerank(questions[r["n"]]["question"], [text[c["chunk"]] for c in top])
    return {"n": r["n"], "results": [top[j] for j in order]}
start = time.time()
# A thread pool sends the requests at the same time: a request waits on the network.
with ThreadPoolExecutor(max_workers=workers) as pool: done = list(pool.map(one, runs))
json.dump(sorted(done, key=lambda r: r["n"]), open(out_path, "w"))
print(f"saved {len(done)} questions to {out_path} | {workers} at the same time, in {time.time() - start:.1f} s")
