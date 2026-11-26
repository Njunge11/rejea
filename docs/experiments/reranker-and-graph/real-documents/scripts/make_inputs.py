"""Make the inputs of the test from the rows of UniDoc-Bench (https://huggingface.co/datasets/Salesforce/UniDoc-Bench).
Use: make_inputs.py <folder with the row files <domain>-0.json and <domain>-100.json> <folder with <domain>/<id>.pdf> <out folder> <domain>...
The out folder gets questions.json (each question with its marked pages), files.json (the document ids), docs.json and pdfs/ (one copy of each PDF).
A document that is in two domains is copied one time."""
import collections, json, os, shutil, sys
rows_dir, pdf_dir, out, domains = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
page = lambda p: (p.split("/")[2], int(os.path.basename(p).rsplit("_", 1)[1].split(".")[0]))      # images/<domain>/<id>/<id>_page_0001.png
qs, docs = [], {}
for dom in domains:
    for part in ("0", "100"):
        d = json.load(open(os.path.join(rows_dir, f"{dom}-{part}.json")))
        for r in (d["rows"] if isinstance(d, dict) and "rows" in d else d):
            r = r.get("row", r)
            for p in r["longdoc_image_paths"]:
                doc, n = page(p); e = docs.setdefault(doc, {"domains": [], "pages": 0}); e["pages"] = max(e["pages"], n)
                if dom not in e["domains"]: e["domains"].append(dom)
            gold = sorted({page(p) for p in r["gt_image_paths"]})
            qs.append({"n": len(qs), "domain": dom, "question": r["question"], "question_type": r["question_type"], "answer_type": r["answer_type"], "gold": [list(g) for g in gold]})
os.makedirs(os.path.join(out, "pdfs"), exist_ok=True)
for doc, e in docs.items(): shutil.copy(os.path.join(pdf_dir, e["domains"][0], doc + ".pdf"), os.path.join(out, "pdfs", doc + ".pdf"))
json.dump(qs, open(os.path.join(out, "questions.json"), "w")); json.dump(sorted(docs), open(os.path.join(out, "files.json"), "w")); json.dump(docs, open(os.path.join(out, "docs.json"), "w"))
print("questions", len(qs), dict(collections.Counter(q["domain"] for q in qs)), "| documents", len(docs), "| pages", sum(e["pages"] for e in docs.values()))
