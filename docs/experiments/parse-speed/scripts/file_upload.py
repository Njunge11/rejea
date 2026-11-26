"""Send PDFs to a Rejea service and confirm all of them at the same time, so that all file jobs wait in the queue together.
Use: file_upload.py <base url> <pdf dir> <names.json> <ids.txt>"""
import json, os, sys, urllib.request
from concurrent.futures import ThreadPoolExecutor
base, pdf_dir, names_path, ids_path = sys.argv[1:5]
names = json.load(open(names_path))
def call(method, url, body=None, headers=None):
    req = urllib.request.Request(url, body, headers or {}, method=method)
    with urllib.request.urlopen(req, timeout=300) as r: return r.read()
def put(name):
    path = os.path.join(pdf_dir, name + ".pdf")
    body = json.dumps({"files": [{"filename": name + ".pdf", "size": os.path.getsize(path), "metadata": {"set": "speed-test"}}]}).encode()
    doc = json.loads(call("POST", base + "/documents", body, {"content-type": "application/json"}))["documents"][0]
    call("PUT", doc["upload_url"], open(path, "rb").read(), {"content-type": "application/pdf"})
    return doc["id"]
with ThreadPoolExecutor(8) as pool: ids = list(pool.map(put, names))
open(ids_path, "w").write("\n".join(ids) + "\n")
with ThreadPoolExecutor(len(ids)) as pool: list(pool.map(lambda i: call("POST", f"{base}/documents/{i}/confirm"), ids))
print("confirmed:", len(ids))
