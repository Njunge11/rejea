# Build checklist

Each item is one change, pushed to `main`. Build them in sequence. Tick the box in the same change.

Sources: the plan ("Document Knowledge Base: Implementation Plan", 10 build steps), the agreed ingest design (three queues, failure and retry flow), and the code in this repository on 2026-11-22.

Rule of each item: `make check` passes, CI is green, and the item has its own proof.

## Built already

- Go service with `migrate` and `serve`, config, logging, HTTP timeouts.
- PostgreSQL 17 with pgvector and pg_textsearch, the five tables, the River tables.
- One River queue with a job time limit, and a test job that writes a row.
- S3 bucket behind CloudFront with signed URLs (AWS, no code reads it yet).
- CI, lint, the commit message check, the test database helper.

## Not decided in the plan

These points are not in the plan. Each one is settled before its item starts.

- **A.** The place of the Next.js app and the Eve agent: this repository or their own. Needed at item 13.
- **B.** The method of the user login. Needed at item 28.
- **C.** The test questions (item 24) and the 30 labelled documents (item 37) come from your documents.
- **D.** The property schemas of the document types `sop`, `guide` and `technical`. The plan has an example for `sop` only. Needed at item 36.
- **E.** The columns of the graph tables. The plan says "following HippoRAG 2". Needed at item 39.
- **F.** The image resolution of a rendered page. Needed at item 16.

## Upload (plan steps 1 and 2)

- [x] **1. Schema for archives**
  - New migration `00003`: column `documents.parent_id`, and `unpacked` in the list of statuses.
  - Proof: `rejea migrate` runs on an empty database.

- [x] **2. Three queues**
  - Queues `file`, `parse` and `index` in place of the one queue.
  - Settings with defaults: worker count of each queue (each starts at 1), job time limit (60 minutes), maximum attempts (8).
  - Proof: a test job runs through each queue.

- [x] **3. HTTP base**
  - All routes in one file. One way to write a JSON answer.
  - One way to turn an error into an HTTP answer, in the `server` package. The body is the problem details object of RFC 9457.
  - One log line for each request.
  - Proof: `/healthz` and an unknown path give the correct answers and log lines.

- [x] **4. File storage**
  - Package `storage`: make a presigned upload URL, download a file as a stream. The size of the file is part of the signature.
  - Config reads the bucket, the region and the life of an upload URL (1 hour).
  - AWS: the IAM role `rejea-service`, with `s3:GetObject` and `s3:PutObject` on the objects of the bucket and no more.
  - Proof: a file goes up through a presigned URL and comes back with the same hash.

- [x] **5. Endpoint: ask for upload URLs**
  - `POST /documents` with a list of files (name, size in bytes, optional metadata). It answers `201` with one presigned URL for each file.
  - One `documents` row for each file, with status `uploading`, the metadata, and the system id in `uploaded_by`.
  - Accepts PDF, Word, zip and tar.
  - One way to read a JSON request: a size limit, no unknown fields, one object, and a clear answer for each fault.
  - Settings with defaults: files in one request (100), size of one file (1 GiB), size of a JSON body (1 MiB).
  - `rejea serve` makes the file storage at its start.
  - Proof: a request for 3 files gives 3 URLs and 3 rows. A request over a limit is refused.

- [x] **6. File worker: hash and duplicate check**
  - The worker downloads the file and computes its hash in one pass. The AWS SDK checks the bytes against the checksum that S3 keeps.
  - The unique `file_hash` finds a duplicate. A duplicate gets the status `duplicate` and stops.
  - `rejea serve` starts River. At a stop, the jobs in work get a set time to finish, then go back to the queue.
  - Settings with defaults: time for jobs to finish at a stop (10 seconds), longest silence of a connection to S3 (1 minute).
  - Proof: a file gets its hash. The same file a second time gets `duplicate`.

- [x] **7. Failure rule for all workers**
  - A stage that fails returns an error, and River tries it again after a wait that grows.
  - A fault that a retry cannot fix stops the job at once: the worker returns `river.JobCancel`.
  - When no attempt is left, or the job is cancelled, the `documents` row gets the status `failed` and the reason in `error`. A job that the stop of the service interrupts does not fail.
  - The rule is one River middleware, so it applies to each worker.
  - A new attempt skips the stages that are complete.
  - Setting with default: time to save a failure (10 seconds).
  - Proof: a job that always fails ends as `failed` with its reason. A job that fails once succeeds on the second attempt.

- [ ] **8. Endpoint: confirm an upload**
  - Sets the status `uploaded` and adds the file job to the `file` queue in one transaction.
  - Proof: a confirm starts the file worker. A confirm of a file that is not in S3 is refused.

- [ ] **9. File worker: unpack archives**
  - A zip or a tar gets the status `unpacked`. Each file inside gets a `documents` row with `parent_id`, and its own job in the `file` queue.
  - Settings with defaults: files in one archive (1,000), unpacked size (1 GB).
  - Proof: a 50-file zip gives 50 rows and 50 jobs. An archive over a limit fails with its reason.

- [ ] **10. Job: fail uploads that were never confirmed**
  - A periodic job sets `failed` on a row that stays `uploading` too long.
  - Setting with default: the age limit (24 hours).
  - Proof: an old `uploading` row becomes `failed`. A new one does not change.

- [ ] **11. Endpoints: status**
  - `GET /documents/{id}`: the status and the error of one file.
  - `GET /documents`: the list, with a `status` filter (`?status=failed`).
  - Proof: each answer agrees with the rows in the database.

- [ ] **12. Endpoint: retry**
  - `POST /documents/{id}/retry` puts the job of a failed file back in its queue. It answers `202 Accepted` with a link to the status.
  - Proof: a failed file runs again after the call. A file that is not failed is refused.

- [ ] **13. Web app base** (needs point A)
  - Next.js app with TanStack Query, and its checks in CI.
  - AWS: the CORS rule of the bucket for the origin of the app.
  - Proof: the app starts and a browser can send a file to a presigned URL.

- [ ] **14. Upload page**
  - Pick PDFs, Word files, a zip or a tar, with optional metadata. The browser sends each file directly to S3 and then confirms.
  - A status list with one line for each file, which TanStack Query keeps current. A failed file shows its reason and a retry button.
  - **Step check of the plan:** a 50-file zip shows a status for every file.

## Parse (plan step 3)

- [ ] **15. Parser service**
  - PaddleOCR and MLX-VLM in a Python virtual environment, with a command that starts the service.
  - Proof: one KNBS table page comes back with block boxes. Record the time for one page on this Mac.

- [ ] **16. Parse worker: render pages** (needs point F)
  - Render each PDF page to an image with PDFium, store it in S3, save `pages` (image key, width, height) and `page_count`.
  - The file worker adds the parse job to the `parse` queue. The status becomes `parsing`.
  - Proof: a 10-page PDF gives 10 images and 10 `pages` rows.

- [ ] **17. Parse port and the PaddleOCR adapter**
  - One small interface for a parser, and the adapter that calls the parser service over HTTP.
  - Proof: the adapter gives the blocks of one page image, each with box, type, text and reading order.

- [ ] **18. Parse worker: blocks**
  - Send each page image to the parser. Save `blocks`. Record the parser name and version on the document.
  - Proof: a PDF has its blocks, with boxes as fractions of the page.

- [ ] **19. Word files**
  - Read `.docx` and `.doc` directly with office_oxide: headings, paragraphs and tables, without coordinates. A Word file skips the render.
  - Proof: a Word file has its blocks, with no page number and no box.
  - **Step check of the plan:** 10 real PDFs (one scanned, one with many tables) and 2 Word files have correct headings, tables and page numbers, and the block boxes agree with the page image.

## Chunk and index (plan step 4)

- [ ] **20. Chunker**
  - Split at headings. Split a long section at paragraphs, merge short ones, keep each chunk under 4,096 tokens. Start each chunk with the title and the heading path.
  - Logic only, with tests. It writes no rows, because a `chunks` row needs its embedding.

- [ ] **21. Embed port, the Cloudflare adapter, the registry and the configurator**
  - One small interface for embeddings, and the adapter for Qwen3-Embedding-0.6B on Cloudflare Workers AI.
  - The registry (provider name to adapter) and the configurator (reads the config and connects them).
  - Retries, timeouts and the usage log are shared code, not adapter code.
  - Proof: a text comes back as 1,024 numbers.

- [ ] **22. Index worker: chunk and embed**
  - The parse worker adds the index job to the `index` queue.
  - The worker makes the chunks, embeds them in batches, and saves `chunks` with pages and block ids. The status becomes `indexed`.
  - Proof: each chunk of a document has text, pages and an embedding.

- [ ] **23. Chunk again without a new parse**
  - A command that makes the chunks of a document again from its saved blocks.
  - **Step check of the plan:** every chunk has text, pages and an embedding. Re-chunking runs without re-parsing.

## Test set (plan step 5)

- [ ] **24. Question file and its script** (needs point C)
  - 30 to 50 questions: fact lookup, reasoning across passages, summary across documents. Each has its expected answer and source page. A few have no answer in the documents.
  - **Step check of the plan:** a script can load them.

## Search (plan step 6)

- [ ] **25. Search endpoint**
  - One query: keyword search and vector search, with a metadata filter. Merge the two lists by reciprocal rank fusion. Return the top chunks with document and page.
  - Proof: a known phrase returns its chunk first.

- [ ] **26. Rerank port and the Cloudflare adapter**
  - One small interface for a reranker, the adapter for bge-reranker-base, and its use in search (a setting turns it on or off).
  - Proof: the order of the results changes with the reranker on.

- [ ] **27. Search report**
  - **Step check of the plan:** the script reports, with and without the reranker, recall at K, MRR and the time for each query.

## Answering (plan step 7)

- [ ] **28. User login** (needs point B)
  - The plan has it in step 1. It was moved to after the upload path. The chat needs it: the agent checks that the user is signed in.
  - The real user id replaces the system id in `uploaded_by`.

- [ ] **29. Eve agent** (needs point A)
  - Its own Node service, on Claude through Eve's Anthropic helper, with `defaultTools` off and a usage cap.
  - One tool, `search`, which calls the Go search endpoint. The agent can call it again with new queries, up to the cap.
  - Instructions: answer only from the results. Each citation has its chunk, document and page.
  - Proof: a question gets an answer with citations.

- [ ] **30. Answer report**
  - **Step check of the plan:** the script reports if each answer and each citation is correct.

## Interface (plan step 8)

- [ ] **31. Chat page**
  - Next.js chat on Eve's `useEveAgent` hook, with citations that a user can click.

- [ ] **32. Endpoint: pages of a document**
  - The size of each page, and a signed CloudFront URL for each page image. Config reads the CloudFront values.
  - Proof: a signed URL opens the image. The same URL without the signature is refused.

- [ ] **33. Page viewer**
  - Shows the page image and draws a box on the blocks of the cited chunk. A toggle hides the boxes.
  - Loads the page sizes first, then only the pages in view and a few ahead.

- [ ] **34. Word viewer**
  - Shows the text of a Word document with the cited section highlighted.
  - **Step check of the plan:** a click on a citation opens the cited page with the correct blocks boxed.

## Types and properties (plan step 9)

- [ ] **35. Chat port and the Claude adapter**
  - One small interface for a chat model, and the adapter for Claude.

- [ ] **36. Document types** (needs point D)
  - Migration: the `doc_types` rows `sop`, `guide` and `technical`, each with its property schema.

- [ ] **37. Classifier** (needs point C)
  - A new first stage of the index worker: pick the `doc_type` from the registry and fill its `properties`. No match becomes `other`.
  - **Step check of the plan:** the classifier is right on 30 hand-labelled documents.

- [ ] **38. Filter tool**
  - A Go endpoint and the Eve tool `filter(doc_type, properties)`, for list and count questions.
  - **Step check of the plan:** "List all SOPs effective after a date" returns the exact rows.

## Knowledge graph (plan step 10)

- [ ] **39. Graph tables** (needs point E)
  - Migration: names, facts and edges, each with embeddings.

- [ ] **40. Extraction**
  - A new stage of the index worker: two LLM calls for each chunk. One lists the names. One writes the facts as name, verb, name.
  - Embed the names and the facts. Link names with near-identical embeddings as the same thing.
  - Proof: a document has its names, facts and edges.

- [ ] **41. Personalized PageRank**
  - Written in Go, on the graph in Postgres. Logic with tests.

- [ ] **42. Graph search tool**
  - A Go endpoint and the Eve tool `graph_search(query)`: find the stored facts nearest to the question, let the LLM drop the irrelevant ones, rank the chunks by Personalized PageRank. No facts left: use plain search.
  - **Step check of the plan:** reasoning and summary questions score higher with `graph_search` than without. Fact lookups score no lower.

## Named earlier, not in the ten steps

- A Dockerfile for a release of the Go service.
- CloudFront logs and a custom domain (AWS only).
- From the "Later" column of the plan: formats beyond PDF and Word, spreadsheets, audio, video, roles and permissions.
