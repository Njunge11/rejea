"""Runs 3 and 4 of the experiment with the code of the HippoRAG 2 authors.
  graph_run.py prep                 build the index: embeddings of the passages, the entities and the facts, then the graph
  graph_run.py dense <out.json>     run 3: vector search only, the best 50 passages for each question
  graph_run.py graph <out.json> N   run 4: graph search for the first N questions, the best 50 passages for each
The saved extraction file of the authors gives the entities and the facts: no model extracts anything here."""
import json, os, sys, time
HERE = os.path.dirname(os.path.abspath(__file__))
REPO = sys.argv[1]; DATA = sys.argv[2]; QUESTIONS = sys.argv[3]; phase = sys.argv[4]
sys.path.insert(0, REPO)
os.environ["TOKENIZERS_PARALLELISM"] = "false"
import logging; logging.basicConfig(level=logging.WARNING)
from src.hipporag.HippoRAG import HippoRAG
from src.hipporag.StandardRAG import StandardRAG
from src.hipporag.utils.config_utils import BaseConfig
from src.hipporag.utils.misc_utils import text_processing, extract_entity_nodes, flatten_facts, reformat_openie_results
import openai

SAVE = os.path.join(HERE, "graph-index"); os.makedirs(SAVE, exist_ok=True)
LLM = "claude-sonnet-5-5"
corpus = json.load(open(f"{DATA}/musique_corpus.json"))
docs = [f"{d['title']}\n{d['text']}" for d in corpus]
number = {d: n for n, d in enumerate(docs)}
questions = json.load(open(QUESTIONS))

def config(key_env, rebuild=False):
    return BaseConfig(save_dir=SAVE, llm_base_url="https://api.anthropic.com/v1", llm_name=LLM, llm_api_key_env=key_env,
        dataset="musique", embedding_model_name="@cf/qwen/qwen3-embedding-0.6b", embedding_provider="openai",
        embedding_base_url=os.environ["EMBED_BASE"], embedding_api_key_env="EMBED_KEY", embedding_batch_size=32,
        temperature=None, seed=None, force_index_from_scratch=rebuild, force_openie_from_scratch=False,
        rerank_dspy_file_path=os.path.join(REPO, "src", "hipporag", "prompts", "dspy_prompts", "filter_llama3.3-70B-Instruct.json"),
        retrieval_top_k=200, linking_top_k=5, qa_top_k=5, openie_mode="online")

def with_retry(model):
    """The provider refuses some requests with a false "input too big" answer: send the request again, then in two halves."""
    import numpy as np
    def plain(texts):
        # The same request as the encode function of the code. The provider gives no token count, so the count is set to 0.
        clean = [(x.replace("\n", " ") or " ") for x in texts]
        r = model.client.embeddings.create(input=clean, model=model.request_model_name, encoding_format="float", timeout=40)
        data = sorted(r.data, key=lambda item: item.index)
        if [item.index for item in data] != list(range(len(texts))): raise ValueError("the provider gave a wrong number of vectors")
        model.last_usage = {"prompt_tokens": 0, "total_tokens": 0}
        return np.asarray([item.embedding for item in data], dtype=np.float32)
    def encode(texts):
        for attempt in range(4):
            try: return plain(texts)
            except (openai.BadRequestError, openai.APIConnectionError, openai.APITimeoutError, openai.RateLimitError, openai.InternalServerError) as e:
                print('  embedding request again:', type(e).__name__, flush=True)
                time.sleep(2 + 3 * attempt)
        if len(texts) == 1: raise RuntimeError("the provider refuses one text as too big: " + texts[0][:80])
        half = len(texts) // 2
        return np.concatenate([encode(texts[:half]), encode(texts[half:])])
    model.encode = encode

def in_slices(store, texts, size, batch, label):
    store.batch_size = batch
    store.embedding_model.embedding_config.encode_params["batch_size"] = batch
    texts = list(dict.fromkeys(texts)); start = time.time()
    for i in range(0, len(texts), size):
        store.insert_strings(texts[i:i + size])
        print(f"  {label}: {min(i + size, len(texts))} of {len(texts)} | {time.time() - start:.0f} s", flush=True)

if phase == "prep":
    os.environ["BLOCKED_KEY"] = "blocked-for-the-index-step"      # a chat call in this step fails: it cannot cost money
    with HippoRAG(global_config=config("BLOCKED_KEY", rebuild=True)) as rag:
        with_retry(rag.embedding_model)
        path = rag.openie_results_path
        if not os.path.isfile(path):
            saved = json.load(open(f"{REPO}/outputs/musique/openie_results_ner_gpt-4o-mini.json"))
            saved["provenance"] = rag._current_openie_provenance()
            saved["note"] = "Entities and facts: extracted with gpt-4o-mini by the HippoRAG 2 authors. The provenance field is added here so that this version of the code loads the file."
            json.dump(saved, open(path, "w"))
        info = json.load(open(path))["docs"]
        assert {x["passage"] for x in info} == set(docs), "the saved extraction does not cover the same passages"
        in_slices(rag.chunk_embedding_store, docs, 2000, 32, "passages")
        ids = [rag.chunk_embedding_store.get_hash_id(d) for d in docs]
        from src.hipporag.utils.misc_utils import compute_mdhash_id
        for x in info: x["idx"] = compute_mdhash_id(x["passage"], "chunk-")
        _, triples = reformat_openie_results(info)
        chunk_triples = [[text_processing(t) for t in triples[i].triples] for i in ids]
        entities, _ = extract_entity_nodes(chunk_triples)
        facts = flatten_facts(chunk_triples)
        print("entities:", len(entities), "| facts:", len(facts), flush=True)
        in_slices(rag.entity_embedding_store, list(entities), 9600, 96, "entities")
        in_slices(rag.fact_embedding_store, [str(f) for f in facts], 9600, 96, "facts")
        rag.index(docs)
        print("graph:", rag.get_graph_info(), flush=True)
elif phase == "dense":
    os.environ["BLOCKED_KEY"] = "blocked-for-the-vector-run"
    with HippoRAG(global_config=config("BLOCKED_KEY")) as rag:      # the same object as the graph run, with the graph not used
        with_retry(rag.embedding_model)
        res = rag.retrieve_dpr(queries=[q["question"] for q in questions], num_to_retrieve=50)
        json.dump([{"n": q["n"], "results": [number[d] for d in r.docs]} for q, r in zip(questions, res)], open(sys.argv[5], "w"))
        print("saved", len(res), "questions")
elif phase == "graph":
    count = int(sys.argv[6]); used = {"calls": 0, "in": 0, "out": 0, "cached": 0}
    with HippoRAG(global_config=config("CHAT_KEY")) as rag:
        with_retry(rag.embedding_model)
        plain = rag.rerank_filter.llm_infer_fn
        def counted(*a, **k):
            if used["calls"] > count + 5: raise RuntimeError("more chat calls than questions: stop")
            out = plain(*a, **k); meta = out[1]
            if len(out) > 2 and out[2]: used["cached"] += 1
            else: used["calls"] += 1; used["in"] += meta.get("prompt_tokens", 0); used["out"] += meta.get("completion_tokens", 0)
            return out
        rag.rerank_filter.llm_infer_fn = counted      # no index step here: the graph is built, and this phase must not extract anything
        path = {"graph": 0, "vector": 0}                # the code uses its vector search when the filter keeps no fact
        g0, d0 = rag.graph_search_with_fact_entities, rag.dense_passage_retrieval
        def g1(*a, **k): path["graph"] += 1; return g0(*a, **k)
        def d1(*a, **k): path["vector"] += 1; return d0(*a, **k)
        rag.graph_search_with_fact_entities, rag.dense_passage_retrieval = g1, d1
        part = questions[:count]
        res = rag.retrieve(queries=[q["question"] for q in part], num_to_retrieve=50)
        print("questions answered by the graph search:", path["graph"], "| by the vector search of the code:", path["vector"])
        json.dump([{"n": q["n"], "results": [number[d] for d in r.docs]} for q, r in zip(part, res)], open(sys.argv[5], "w"))
        cost = used["in"] / 1e6 * 2 + used["out"] / 1e6 * 10
        print(f"saved {len(res)} questions | new chat calls {used['calls']}, from the local cache {used['cached']} | input tokens {used['in']}, output tokens {used['out']} | cost ${cost:.3f}")
