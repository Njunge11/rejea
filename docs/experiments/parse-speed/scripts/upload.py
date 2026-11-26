"""Send PDFs to a Rejea service with its own upload flow: ask for upload URLs, PUT each file, confirm each document.
Use: upload.py <base url> <pdf dir> <names.json> <ids.json>. A name that is in ids.json already is not sent again."""
import json, os, sys, urllib.request
base, pdf_dir, names_path, ids_path = sys.argv[1:5]
names = json.load(open(names_path))
try: ids = json.load(open(ids_path))
except FileNotFoundError: ids = {}
def call(method, url, body=None, headers=None):
    req = urllib.request.Request(url, body, headers or {}, method=method)
    with urllib.request.urlopen(req, timeout=300) as r: return r.status, r.read()
for name in names:
    if name in ids: continue
    path = os.path.join(pdf_dir, name + ".pdf"); size = os.path.getsize(path)
    body = json.dumps({"files": [{"filename": name + ".pdf", "size": size, "metadata": {"set": "unidoc-finance"}}]}).encode()
    _, out = call("POST", base + "/documents", body, {"content-type": "application/json"})
    doc = json.loads(out)["documents"][0]
    call("PUT", doc["upload_url"], open(path, "rb").read(), {"content-type": "application/pdf"})
    status, out = call("POST", f"{base}/documents/{doc['id']}/confirm")
    ids[name] = doc["id"]; json.dump(ids, open(ids_path, "w"))
    print(name, "->", json.loads(out)["status"], flush=True)
