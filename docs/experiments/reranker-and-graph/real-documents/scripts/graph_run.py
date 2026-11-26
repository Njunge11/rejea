"""Graph runs on Rejea's chunks, with the code of the HippoRAG 2 authors.
  graph_run.py <HippoRAG repo> <chunks.json> <questions.json> <save dir> build              a chat model reads the names and the facts out of each chunk, then the code builds the graph
  graph_run.py <HippoRAG repo> <chunks.json> <questions.json> <save dir> dense <out.json>   the vector search of the code, graph not used: the best 50 chunks for each question
  graph_run.py <HippoRAG repo> <chunks.json> <questions.json> <save dir> graph <out.json>   the graph search: the best 50 chunks for each question
Settings: GRAPH_BUILD_MODEL (default claude-haiku-5-5), GRAPH_SEARCH_MODEL (default claude-sonnet-5-5), GRAPH_LOST_LIMIT (default 0.01), GRAPH_CHAT_WORKERS, GRAPH_VECTOR_WORKERS, GRAPH_VECTOR_BATCH (defaults 8, 8, 32), CHAT_KEY, EMBED_BASE, EMBED_KEY.
PRICES gives US dollars for one million input and output tokens (Anthropic's price page, 2026-11-26)."""
import json, os, sys, time
REPO, CHUNKS, QUESTIONS, SAVE, phase = sys.argv[1:6]
BUILD = os.environ.get("GRAPH_BUILD_MODEL", "claude-haiku-5-5"); SEARCH = os.environ.get("GRAPH_SEARCH_MODEL", "claude-sonnet-5-5")
CHAT_WORKERS = int(os.environ.get("GRAPH_CHAT_WORKERS", "8"))        # chat calls at the same time, in the build
VECTOR_WORKERS = int(os.environ.get("GRAPH_VECTOR_WORKERS", "8"))    # vector requests at the same time
VECTOR_BATCH = int(os.environ.get("GRAPH_VECTOR_BATCH", "32"))       # texts in one vector request
BUILD_EXTRA = json.loads(os.environ.get("GRAPH_BUILD_EXTRA", "{}"))  # extra fields of each chat request of the build, such as {"output_config": {"effort": "low"}}
CHAT_RETRIES = int(os.environ.get("GRAPH_CHAT_RETRIES", "8"))      # new attempts of a chat call that the provider refuses at its rate limit: the client waits (it obeys Retry-After) and sends the call again
TIME_LOG = os.environ.get("GRAPH_TIME_LOG")      # a file that gets one line for each chat call and each vector request: the kind, the start, the end, the output tokens
HTTP_LOG = os.environ.get("GRAPH_HTTP_LOG")      # a file that gets the status of each HTTP request, to count the requests that a provider refuses
LOST_LIMIT = float(os.environ.get("GRAPH_LOST_LIMIT", "0.01"))      # the build stops when the chunks with no readable answer hold more than this share of all words
PRICES = {"claude-haiku-5-5": (0.10, 0.50), "claude-sonnet-5-5": (2.0, 10.0)}
sys.path.insert(0, REPO)
os.environ["TOKENIZERS_PARALLELISM"] = "false"
import logging, threading; logging.basicConfig(level=logging.WARNING)
if HTTP_LOG:
    _h = logging.FileHandler(HTTP_LOG); _h.setFormatter(logging.Formatter("%(asctime)s %(message)s")); _x = logging.getLogger("httpx"); _x.setLevel(logging.INFO); _x.addHandler(_h); _x.propagate = False
_time_lock = threading.Lock()
def timed(kind, start, tokens=0):
    if TIME_LOG:
        with _time_lock: open(TIME_LOG, "a").write(json.dumps({"kind": kind, "start": round(start, 3), "end": round(time.time(), 3), "out": tokens}) + "\n")
from src.hipporag.HippoRAG import HippoRAG
from src.hipporag.utils.config_utils import BaseConfig
from src.hipporag.llm.openai_gpt import CacheOpenAI
import openai

os.makedirs(SAVE, exist_ok=True)
chunks = json.load(open(CHUNKS)); questions = json.load(open(QUESTIONS))
by_text = {}
for c in chunks: by_text.setdefault(c["text"], c)      # the code keeps one copy of equal texts
docs = list(by_text)

def config(model, key_env, rebuild=False):
    # llm_name stays the build model in each phase: the code refuses a saved graph when this name changes.
    return BaseConfig(save_dir=SAVE, llm_base_url="https://api.anthropic.com/v1", llm_name=model, llm_api_key_env=key_env,
        dataset="unidoc", embedding_model_name="@cf/qwen/qwen3-embedding-0.6b", embedding_provider="openai",
        embedding_base_url=os.environ["EMBED_BASE"], embedding_api_key_env="EMBED_KEY", embedding_batch_size=32,
        temperature=None, seed=None, force_index_from_scratch=rebuild, force_openie_from_scratch=False,
        rerank_dspy_file_path=os.path.join(REPO, "src", "hipporag", "prompts", "dspy_prompts", "filter_llama3.3-70B-Instruct.json"),
        retrieval_top_k=200, linking_top_k=5, qa_top_k=5, openie_mode="online", openie_max_workers=CHAT_WORKERS)

def with_retry(model):
    """The provider refuses some requests with a false "input too big" answer: send the request again, then in two halves."""
    import numpy as np, threading
    from concurrent.futures import ThreadPoolExecutor
    sent = {"requests": 0, "texts": 0, "seconds": 0.0}; lock = threading.Lock()
    def plain(texts):
        # The same request as the encode function of the code. The provider gives no token count, so the count is set to 0.
        clean = [(x.replace("\n", " ") or " ") for x in texts]
        t0 = time.time(); r = model.client.embeddings.create(input=clean, model=model.request_model_name, encoding_format="float", timeout=40); timed("vector", t0, len(texts))
        with lock: sent["requests"] += 1; sent["texts"] += len(texts)
        data = sorted(r.data, key=lambda item: item.index)
        if [item.index for item in data] != list(range(len(texts))): raise ValueError("the provider gave a wrong number of vectors")
        model.last_usage = {"prompt_tokens": 0, "total_tokens": 0}
        return np.asarray([item.embedding for item in data], dtype=np.float32)
    def encode(texts):
        for attempt in range(4):
            try: return plain(texts)
            except (openai.BadRequestError, openai.APIConnectionError, openai.APITimeoutError, openai.RateLimitError, openai.InternalServerError) as e:
                print("  embedding request again:", type(e).__name__, str(e)[:160], flush=True)
                time.sleep(2 + 3 * attempt)
        if len(texts) == 1: raise RuntimeError("the provider refuses one text as too big: " + texts[0][:80])
        half = len(texts) // 2
        return np.concatenate([encode(texts[:half]), encode(texts[half:])])
    def batch_encode(texts, **kwargs):
        # The code sends one vector request after the other. Here the requests go at the same time, from a pool of threads.
        # A thread pool is the standard primitive of Python for calls that wait on a network (documentation of concurrent.futures). pool.map keeps the order.
        if isinstance(texts, str): texts = [texts]
        start = time.time(); batches = [texts[i:i + VECTOR_BATCH] for i in range(0, len(texts), VECTOR_BATCH)]
        with ThreadPoolExecutor(max_workers=VECTOR_WORKERS) as pool: parts = list(pool.map(encode, batches))
        with lock: sent["seconds"] += time.time() - start
        return model._normalize_embeddings(np.concatenate(parts), normalize=kwargs.get("norm"))
    model.encode = encode; model.batch_encode = batch_encode
    return sent

def counted(llm, used):
    plain = llm.infer
    def infer(*a, **k):
        t0 = time.time(); out = plain(*a, **k); meta = out[1]
        if len(out) > 2 and out[2]: used["cached"] += 1
        else: timed("chat", t0, meta.get("completion_tokens", 0)); used["calls"] += 1; used["in"] += meta.get("prompt_tokens", 0); used["out"] += meta.get("completion_tokens", 0)
        return out
    return infer

def tolerant(ie, lost):
    """The code stops the full build when one answer of the chat model is not in the demanded form. A text with almost no words gets such an answer.
    Here a chunk with an answer that cannot be read gets no names or no facts, and the build continues. A call with no answer (a failed request) stays a failure."""
    ner0, facts0 = ie.ner, ie.triple_extraction
    def note(kind, passage, out):
        lost.append({"step": kind, "words": len(passage.split()), "error": str(out.metadata.pop("error"))[:120], "answer": out.response[:80]})
        out.metadata["not read"] = True
    def ner(chunk_key, passage, *a, **k):
        out = ner0(chunk_key, passage, *a, **k)
        if out.metadata.get("error") and out.response: note("names", passage, out)
        return out
    def facts(chunk_key, passage, *a, **k):
        out = facts0(chunk_key, passage, *a, **k)
        if out.metadata.get("error") and out.response: note("facts", passage, out)
        return out
    ie.ner, ie.triple_extraction = ner, facts

def cost(model, used): return used["in"] / 1e6 * PRICES[model][0] + used["out"] / 1e6 * PRICES[model][1]
def rows(res): return [{"n": q["n"], "results": [{k: by_text[d][k] for k in ("chunk", "doc", "page_start", "page_end")} for d in r.docs]} for q, r in zip(questions, res)]

if phase == "build":
    used = {"calls": 0, "in": 0, "out": 0, "cached": 0}; start = time.time()
    with HippoRAG(global_config=config(BUILD, "CHAT_KEY", rebuild=True)) as rag:
        sent = with_retry(rag.embedding_model)
        if BUILD_EXTRA: rag.extraction_llm.llm_config.generate_params["extra_body"] = BUILD_EXTRA
        rag.extraction_llm.openai_client.max_retries = CHAT_RETRIES
        rag.extraction_llm.infer = counted(rag.extraction_llm, used)
        lost = []; tolerant(rag.openie, lost)
        batch0 = rag.openie.batch_openie; chat_seconds = [0.0]
        def checked(*a, **k):      # directly after the chat calls: stop here when too much text is lost, before the vectors of the names and the graph
            t0 = time.time(); out = batch0(*a, **k); chat_seconds[0] += time.time() - t0
            share = sum(x["words"] for x in lost) / max(1, sum(len(d.split()) for d in docs))
            if share > LOST_LIMIT: sys.exit(f"STOP: the chat model gave no readable answer for chunks with {100 * share:.1f}% of all words (limit {100 * LOST_LIMIT:.0f}%): {json.dumps(lost[:5])}")
            return out
        rag.openie.batch_openie = checked
        rag.index(docs)
        print("graph:", rag.get_graph_info(), flush=True)
        print("answers not read:", len(lost), "| words in those chunks:", [x["words"] for x in lost], "| steps:", [x["step"] for x in lost], flush=True)
    print(json.dumps({"phase": "build", "model": BUILD, "chunks": len(chunks), "different texts": len(docs), "seconds": round(time.time() - start), "chat calls": used["calls"], "from the local cache": used["cached"], "input tokens": used["in"], "output tokens": used["out"], "chat cost usd": round(cost(BUILD, used), 4), "vector requests": sent["requests"], "vector texts": sent["texts"], "chat seconds": round(chat_seconds[0]), "vector seconds": round(sent["seconds"]), "chat workers": CHAT_WORKERS, "vector workers": VECTOR_WORKERS, "vector batch": VECTOR_BATCH}))
elif phase == "dense":
    os.environ["BLOCKED_KEY"] = "blocked-for-the-vector-run"      # a chat call in this phase fails: it cannot cost money
    with HippoRAG(global_config=config(BUILD, "BLOCKED_KEY")) as rag:
        with_retry(rag.embedding_model)
        res = rag.retrieve_dpr(queries=[q["question"] for q in questions], num_to_retrieve=50)
    json.dump(rows(res), open(sys.argv[6], "w")); print("saved", len(res), "questions to", sys.argv[6])
elif phase == "graph":
    used = {"calls": 0, "in": 0, "out": 0, "cached": 0}
    with HippoRAG(global_config=config(BUILD, "CHAT_KEY")) as rag:
        with_retry(rag.embedding_model)
        # The search model selects the facts where the graph search starts. The graph is built: this phase reads no names out of chunks.
        search_llm = rag.extraction_llm if SEARCH == BUILD else CacheOpenAI.from_experiment_config(config(SEARCH, "CHAT_KEY"))
        rag.rerank_filter.llm_infer_fn = counted(search_llm, used)
        path = {"graph": 0, "vector": 0}                # the code uses its vector search when the filter keeps no fact
        g0, d0 = rag.graph_search_with_fact_entities, rag.dense_passage_retrieval
        def g1(*a, **k): path["graph"] += 1; return g0(*a, **k)
        def d1(*a, **k): path["vector"] += 1; return d0(*a, **k)
        rag.graph_search_with_fact_entities, rag.dense_passage_retrieval = g1, d1
        res = rag.retrieve(queries=[q["question"] for q in questions], num_to_retrieve=50)
    json.dump(rows(res), open(sys.argv[6], "w"))
    print(json.dumps({"phase": "graph", "model": SEARCH, "questions": len(res), "by the graph search": path["graph"], "with no start point (vector search of the code used)": len(res) - path["graph"], "chat calls": used["calls"], "from the local cache": used["cached"], "input tokens": used["in"], "output tokens": used["out"], "chat cost usd": round(cost(SEARCH, used), 4)}))
else:
    sys.exit("STOP: the phase is build, dense or graph")
