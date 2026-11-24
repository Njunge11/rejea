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

- [x] **8. Endpoint: confirm an upload**
  - `POST /documents/{id}/confirm` sets the status `uploaded` and adds the file job to the `file` queue in one transaction. It answers `202` with the place of the status.
  - A second confirm of the same document changes nothing and gets the same answer.
  - AWS: the role gets `s3:ListBucket`, so that S3 can tell a missing file from a refused request.
  - Proof: a confirm starts the file worker. A confirm of a file that is not in S3 is refused.

- [x] **9. File worker: unpack archives**
  - A zip, a tar or a tar.gz gets the status `unpacked`. Each PDF and Word file inside gets a `documents` row with `parent_id`, its hash, and its own job in the `file` queue. A file inside with the same content as a different document gets the status `duplicate`.
  - The service uses no disk. It reads a zip from S3 in pieces, and a tar as one stream. It sends each file inside to S3 as a stream.
  - Other entries are skipped: folders, links, hidden files, other file types, and an archive in an archive.
  - An archive over a limit, a damaged archive, an encrypted zip, and an archive with no PDF or Word file fail at once with the reason. The service then removes the files that it sent to S3.
  - Settings with defaults: files in one archive (1,000), unpacked size (1 GB), size of the file list of a zip (16 MiB), size of one piece of a read from S3 (8 MiB).
  - AWS: the role gets `s3:DeleteObject` and `s3:AbortMultipartUpload`.
  - Proof: a 50-file zip gives 50 rows and 50 jobs. An archive over a limit fails with its reason.

- [x] **10. Job: fail uploads that were never confirmed**
  - A periodic job of River sets `failed` on a row that stays `uploading` too long, and removes its file from S3 if the client sent one.
  - River adds the job at a fixed interval and at each start of the service. With many copies of the service, only the leader adds it.
  - The job runs in its own queue, `maintenance`.
  - Settings with defaults: the age limit (24 hours), the interval (1 hour). The age limit must be longer than the life of an upload URL, or the service does not start.
  - Proof: an old `uploading` row becomes `failed`. A new one does not change.

- [x] **11. Endpoints: status**
  - `GET /documents/{id}`: the status and the error of one file.
  - `GET /documents`: the list, with a `status` filter (`?status=failed`).
  - Proof: each answer agrees with the rows in the database.

- [x] **12. Endpoint: retry**
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

- [x] **15. Parser service**
  - PaddleOCR and MLX-VLM in a Python virtual environment, with a command that starts the service.
  - Proof: one KNBS table page comes back with block boxes. Record the time for one page on this Mac.
  - Measured on this Mac (M2 Pro, 16 GB), PaddleOCR-VL-1.6, 2026-11-23:
    - A page with two paragraphs and a table of 18 rows (consumer prices, March 2026, page 10): 12 seconds. All 126 numbers agree with the text in the PDF, at 144 and at 216 DPI.
    - A page with one turned table of 23 columns (GDP, first quarter 2026, page 11): 40 seconds at 216 DPI, and all 578 numbers agree. At 144 DPI it takes 32 seconds, and 108 numbers are missing and 21 are wrong.
    - Memory: 2.0 GB for the MLX server and 0.9 GB for the parse API. Start with the models on the disk: 20 seconds.

- [x] **16. Parse worker: render pages**
  - Render each PDF page to an image with PDFium, store it in S3, save `pages` (image key, width, height) and `page_count`.
  - The file worker adds the parse job to the `parse` queue. The status becomes `parsing`.
  - PDFium is the WebAssembly build in the Go module go-pdfium: no C library on the machine, and PDFium has no access to the machine.
  - The service uses no disk: PDFium reads the PDF from S3 in pieces. An image is a PNG with the key `documents/<id>/pages/<n>.png`.
  - An image shows the page as a PDF viewer shows it: on white paper, with its annotations and the values in the fields of a form.
  - A page is recorded when its image is in S3, so a new attempt renders only the pages that are not recorded. `page_count` is recorded last.
  - A file that is not a PDF, a PDF with a password and a PDF with no page fail at once with the reason.
  - The time limit of a job stops a render that is in work.
  - Settings with defaults: resolution of a page image (216 DPI), pixels in one page image (100,000,000; a larger page gets a lower resolution).
  - Proof: a 10-page PDF gives 10 images and 10 `pages` rows.
  - Measured on this Mac (M2 Pro), 2026-11-24:
    - A 10-page PDF: 5 seconds, of which the send to S3 from this network is the largest part. The render and the PNG of one page take 0.2 seconds.
    - The parser reads the images of the two KNBS pages of item 15 with the same result: all 578 numbers of the GDP page and all 126 numbers of the consumer price page agree with the PDF.
    - Memory of the service: 59 MB with no work, and about 130 MB more for each PDF in work. The start of the service takes 1 second more.

- [x] **17. Parse port and the PaddleOCR adapter**
  - One small interface for a parser, and the adapter that calls the parser service over HTTP.
  - Package `parse`: the port `Parser` (a page image in, its blocks out, in reading order) and the adapter `PaddleOCR`.
  - A block has one of 12 types: title, heading, paragraph, table, figure, caption, formula, footnote, header, footer, page_number, aside. The adapter gives each of the 25 labels of the layout model its type, and fails on a label that it does not know.
  - A box is four fractions of the page, from 0 to 1. A table is HTML. A chart is the values that the parser read from it.
  - Each failure of a call is an error that a new attempt can correct. The adapter does no retry: River does.
  - Settings with defaults: address of the parser (`http://127.0.0.1:8112`), time limit for one page (5 minutes), size of an answer (16 MiB), read the values of charts (true).
  - Fix of the parser service of item 15: the vision model server reads one block at a time. With its default, mlx-vlm 0.8.0 failed on a page with many blocks, and the parse API answered 500 for 3 of 18 requests. The speed is the same.
  - Proof: the adapter gives the blocks of one page image, each with box, type, text and reading order.
  - Measured on this Mac (M2 Pro), 2026-11-24:
    - The 13 pages of the consumer price report of March 2026: 111 seconds with charts on, 3 to 13 seconds a page, no failure.
    - The numbers that came through the adapter agree with the PDF: all 126 of the consumer price table page and all 578 of the GDP table page. The boxes agree with the page image.
    - Charts: 5 charts of the report and 2 test charts (one with no data labels) gave each value correctly. A page with a chart takes 3 to 5 seconds more.

- [ ] **18. Parse worker: blocks**
  - Send each page image to the parser. Save `blocks`. Record the parser name and version on the document.
  - A second stage of the parse worker of item 16, after the render. It sends the PNG that item 16 stored in S3, not a new render.
  - `rejea serve` makes the adapter of item 17 from `cfg.Parse` and gives it to the workers. No code calls it before this item.
  - Migration: the comment of `blocks.type` gets the 12 types of the package `parse`.
  - The parse API has no version in its answers. The name of the model is in `parser/pipeline.yaml`, and the versions are in `parser/constraints-paddle.txt`.
  - The blocks of a page are saved when the parser answers, so a new attempt parses only the pages with no blocks. The parser takes 8 to 40 seconds for a page on this Mac, and one attempt of a job has 60 minutes.
  - Proof: a PDF has its blocks, with boxes as fractions of the page.

- [ ] **19. Word files**
  - Read `.docx` and `.doc` directly with office_oxide: headings, paragraphs and tables, without coordinates. A Word file skips the render.
  - A Word file has its parse job and the status `parsing` from item 16. The parse worker returns at once for it now: this item adds the Word stage at that place.
  - Same methods as item 16: a fault in the content of the file fails the document at once with the reason, a new attempt skips a stage that is complete, and each change of a document goes through `documents`.
  - Proof: a Word file has its blocks, with no page number and no box.
  - **Step check of the plan:** 10 real PDFs (one scanned, one with many tables) and 2 Word files have correct headings, tables and page numbers, and the block boxes agree with the page image.

## Chunk and index (plan step 4)

- [ ] **20. Chunker**
  - Split at headings. Split a long section at paragraphs, merge short ones, keep each chunk under 4,096 tokens. Start each chunk with the title and the heading path.
  - Logic only, with tests. It writes no rows, because a `chunks` row needs its embedding.
  - The parser gives no level for a heading. Its API has a second endpoint, `/restructure-pages`, which sets the levels of the headings across the pages of a document and joins a table that continues on the next page. Decide here if the chunker uses it.
  - The blocks of the types header, footer, page_number and aside are not body text. PaddleOCR leaves them, and footnotes, out of its own Markdown.

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
