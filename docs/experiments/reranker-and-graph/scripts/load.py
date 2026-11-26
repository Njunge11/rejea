"""Write the passages of a public test set as rows for the documents and blocks tables (one document for each passage)."""
import csv, json, sys, uuid
corpus_path, name, out, first, count = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5])
corpus = json.load(open(corpus_path))
ns = uuid.UUID("5b0c1f8e-2f61-4d7b-9a55-0a1d2c3e4f50")
with open(f"{out}/documents.csv", "w", newline="") as d, open(f"{out}/blocks.csv", "w", newline="") as b:
    dw, bw = csv.writer(d), csv.writer(b)
    for n in range(first, min(first + count, len(corpus))):
        p = corpus[n]
        doc = uuid.uuid5(ns, f"{name}-{n}")
        fn = f"{name}-{n:05d}.docx"
        dw.writerow([doc, "00000000-0000-0000-0000-000000000000", fn, f"bench/{fn}", p["title"], json.dumps({"set": name, "n": n}), "indexed", "bench-loader"])
        bw.writerow([uuid.uuid5(ns, f"{name}-{n}-h"), doc, 1, "heading", p["title"], 1])
        bw.writerow([uuid.uuid5(ns, f"{name}-{n}-p"), doc, 2, "paragraph", p["text"], ""])
print("rows written for passages", first, "to", min(first + count, len(corpus)) - 1)
