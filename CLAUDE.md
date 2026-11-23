# Layers

`server`, `ingest` → `documents` → `database`

- `server`: HTTP only. No decision.
- `ingest`: file work only (download, hash, unpack). Each change of a document goes through `documents`.
- `documents`: each rule. No HTTP type, no SQL.
- `database`: SQL only. No decision: each value that it writes comes from its caller.
