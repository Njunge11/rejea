# Build checklist

Each item is one change, pushed to `main`. Build them in sequence. Tick the box in the same change.

Sources: the plan ("Document Knowledge Base: Implementation Plan", 10 build steps), the agreed ingest design (three queues, failure and retry flow), and the code in this repository on 2026-11-22.

Rule of each item: `make check` passes, CI is green, and the item has its own proof. After each item, `make up` on a new clone still starts all of Rejea: a new part that the service needs goes into `compose.yaml`, the image of the service or `scripts/up` in the same change.

## Built already

- Go service with `migrate` and `serve`, config, logging, HTTP timeouts.
- PostgreSQL 17 with pgvector and pg_textsearch, the five tables, the River tables.
- One River queue with a job time limit, and a test job that writes a row.
- S3 bucket behind CloudFront with signed URLs (AWS, no code reads it yet).
- CI, lint, the commit message check, the test database helper.

## Setup

- [x] **One command for a new clone: `make up`**
  - Docker Compose starts the database, the file store and the service, each with a health check. The migrations run in a container that stops before the service starts. `scripts/up` first names each tool that is missing, and it starts the parser on a Mac. `make down` stops all of it.
  - No settings file is necessary: each setting has a default. A later item that needs a value with no default (an API key) adds it to the check of `scripts/up`.
  - The image of the service (`Dockerfile`): two stages, Docker Official base images with version and digest, a user that is not root. It has the tokenizers library, the tokenizer of the model and the Word reader.
  - The file store is SeaweedFS 4.55, with the API of S3. The storage package has two new settings: `REJEA_S3_ENDPOINT`, and `REJEA_S3_PUBLIC_ENDPOINT` for the address in an upload URL. With no endpoint, the bucket is in AWS S3 as before.
  - Choice of the store: SeaweedFS, RustFS 1.0.6 and the Versity gateway 1.11.0 each passed the same 18 calls of the storage package (upload URL with a right and a wrong size, exists, download, a 20 MiB upload in parts, range reads, an upload that fails, delete). Only SeaweedFS makes its bucket from a setting and answers the preflight request of a browser with no more setup. Its cost: an image of 280 MB to download and 260 MB of memory, against 24 MB of memory for the Versity gateway. MinIO has no more releases.
  - The parser stays on the Mac: Docker gives a container no GPU on a Mac. The service reaches it at `host.docker.internal`.
  - Proof: a copy of the repository with only its committed files and no `.env`, in an empty folder, with empty volumes: `make up` ended with exit 0 after 168 seconds. Then `heading123.docx` (6 blocks) and `labour.pdf` (13 pages, 97 blocks, 118 seconds) went through upload URL, upload, confirm and parse. The files and the rows stayed after a restart of the store and the service. A second `make up` took 6 seconds. `make down` stopped the containers and the two parser processes. With no Docker, `make up` names it and stops.
  - Proof of a first run: a new `git clone` from GitHub, with no Rejea image, an empty Docker build cache, an empty pip cache and empty model caches: `make up` ended with exit 0 after 1,089 seconds (containers 303, parser packages 255, models and parser start 531). The same two files went through with the same block counts.
  - A second `scripts/parser start` while the parser starts does not start a second parser: it waits for the first.
  - Each fault gives a message with the fault and its correction ("To correct: ..."). `make up` examines the machine first and names all faults at one time. The service stops at its start, not at the first request: `storage.New` asks the store for the bucket, and `database.Current` compares the schema version. The texts of the corrections are in one file, `cmd/rejea/advice.go`.
  - Proof, each fault caused and its message read. The service (19): no settings; two wrong values; database URL of a wrong form, no database at the address, wrong password, wrong database name, no schema; store with no bucket setting, an address with no `http://`, no keys, a wrong key, no store at the address, a bucket that is not there; port in use; parser and Word reader not there (warnings); `migrate` with no database; the AWS settings still start. `make up` (9): Docker stopped, Docker not installed, a port in use (it names the program), a port value that is not a number, Python not there, a wrong Python version, low disk space, a wrong value in `.env` that stops a container, other ports from `.env`. `make` (3): no Go, no golangci-lint, no tokenizers library (the target installs it).
  - The output of `make up` obeys the Command Line Interface Guidelines (clig.dev): a list of what the run installs with the sizes, 4 numbered steps with their times, one status line that moves for each long step (the packages, the model download with its size, the parser start), and the log of a step only when the step fails. Colour, the banner and the status line are for a terminal only; `NO_COLOR` turns colour off. The look is in one file, `scripts/ui.sh`, with no tool to install.
  - Proof of the output, each run recorded in a terminal session (`script`): a first run with no parser packages and no model (exit 0 after 10 min 19 s, 433 updates of the status line in place), a later run (35 s), the faults, a run in a pipe (no escape codes), `NO_COLOR` (no colour codes), a stop signal during the model download (the download stops), and `scripts/parser stop` while the model server downloads (no process stays).
  - Not proved: a Mac with an Intel processor. The messages for a wrong AWS Region and for a schema that is newer than the service are written, not caused. CI runs `make up` on Linux (amd64) for each push.
  - Not built: a parser on Linux, and Windows.

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

- [x] **18. Parse worker: blocks**
  - Send each page image to the parser. Save `blocks`. Record the parser name and version on the document.
  - A second stage of the parse worker of item 16, after the render. It sends the PNG that item 16 stored in S3, not a new render.
  - `rejea serve` makes the adapter of item 17 from `cfg.Parse` and gives it to the workers.
  - The blocks of a page and the time of its parse (`pages.parsed_at`) are saved in one transaction when the parser answers. A new attempt parses only the pages with no `parsed_at`. A blank page is parsed and has no block.
  - `documents.parser` is saved last: it tells that the stage is complete. The document keeps the status `parsing`, because the chunk and embed stages come next.
  - The parse API has no version in its answers. Thus the parser name is a setting, `REJEA_PARSER_NAME`, with the default `PaddleOCR-VL-1.6` of `parser/pipeline.yaml`.
  - `blocks.seq` is the place in the reading order of the page, from 1. A unique index on (document, page, seq) gives the blocks in reading order.
  - Migration 4: `pages.parsed_at`, the index, and the comment of `blocks.type` with the 12 types of the package `parse`.
  - While the parser does not run, the parse job fails and River runs it again after a wait. After the last attempt the document fails with the reason, and a retry continues from the pages that are left.
  - Proof: a PDF has its blocks, with boxes as fractions of the page.
  - Measured on this Mac (M2 Pro), 2026-11-24:
    - The consumer price report of March 2026: 13 pages, 138 blocks, 2 minutes. Each page has the same number of blocks as in item 17. Each box is inside its page.
    - The saved blocks of the price table page have all 126 numbers of the PDF.
    - A PDF with a blank page: the blank page is parsed and has no block.
    - A stop of the service at page 4 of 10: the new start parses the 6 pages that are left.
    - A second run of a document that has its blocks parses nothing.
    - A zip with two PDFs and two parse workers: each PDF gets its blocks.
    - A Word file gets no block: item 19.
- [x] **19. Word files**
  - Read `.docx` and `.doc` directly with office_oxide: headings, paragraphs, tables and lists, without coordinates. A Word file skips the render.
  - The Word stage of the parse worker: it gives the file to the Word reader and saves the blocks and the name of the reader in one transaction. The blocks have no page and no box. `blocks.seq` is the place in the reading order of the file.
  - The reader is the command line program of office_oxide 0.1.17, one process for each file, with the strict limits of the library. Reason: the guide of the library says that it has no time limit, and that a file from an unknown source is read under a time limit or in a process of its own. Thus the build needs no CGO, the service stops a process that takes too long, and a fault in the program does not stop the service.
  - `make word-install` downloads the program of one release to `bin/` and checks its SHA-256 hash.
  - The program reads a file from the disk only. The reader makes a copy of the file in the folder for files of a short life, and removes it when the program ends.
  - Package `parse`: `Word` (the reader), the block type `list`, and `Block.Level` for the level of a heading. Migration 5: `blocks.level`.
  - What the reader leaves out: the text at the top and at the bottom of each page, the comments of reviewers, and a picture with no description.
  - Failure: a file that the program cannot read (not a Word file, a password, damage, a limit, the time limit) fails the document at once with the reason. While the program is not installed, the job fails and River runs it again.
  - Settings with defaults: the program (`bin/office-oxide`), the time limit for one file (1 minute), the folder for the copy (that of the operating system).
  - Proof: a Word file has its blocks, with no page number and no box.
  - Measured on this Mac (M2 Pro), 2026-11-24, with 9 public Word files of Apache POI:
    - Each file has its blocks in 5 to 35 ms. Each word of the plain text of the file is in the blocks, but for the comments of reviewers.
    - A `.docx` with headings of levels 1, 2 and 3 has these levels. A `.doc` with a table has the table as HTML. A file with 6 tables, 2 headings and 1 footnote has the same counts as its XML.
    - A file with a password and a file that is not a Word file fail at attempt 1 with the reason.
    - A zip with two Word files: each has its blocks. A second run reads nothing again. No copy of a file stays on the disk.
    - Facts of the library: it gives heading level 6 for a level 7 heading, and it gives each list of a `.doc` file as a numbered list.
  - **Step check of the plan:** 10 real PDFs (one scanned, one with many tables) and 2 Word files have correct headings, tables and page numbers, and the block boxes agree with the page image.
  - Result of the step check, 2026-11-24. The 10 PDFs are KNBS reports with 57 pages in total. All 12 files went through the service in 703 seconds with no error.
    - The 9 PDFs with a text layer: 96.9% to 100% of the words of the PDF are in the blocks of the same page, and 95.9% to 100% of the numbers.
    - Boxes: for 299 of 306 blocks, the text of the PDF in the box is the text of the block.
    - The scanned PDF has no text layer to compare. On its page image, the boxes fit, and the heading, the table and its numbers are correct.
    - Page images of 5 PDFs with the boxes drawn: the headings, tables, footers and page numbers have the correct types.
    - The 2 Word files have the headings, the levels, the tables and the footnote of their XML.
    - Fix from this check: the parser did not read the text in pictures. One infographic report had only 86.4% of its words. The setting `REJEA_PARSER_READ_IMAGE_TEXT` (default true) raises it to 96.9%, and that report takes 37% more time.

- [x] **43. Markdown files** (added 2026-11-27, not in the ten steps of the plan)
  - Read `.md` and `.markdown` files with goldmark 1.8.7, the Markdown library of Hugo: CommonMark, with the additions of GitHub (tables, struck text, task lists) and footnotes. No parser and no other program. A Markdown file skips the render, as a Word file does.
  - The method is that of Unstructured, which Docling also uses for a file with HTML in it: goldmark turns the Markdown into HTML, and one HTML reader makes the blocks. Thus a table, a list or a heading that a file has as HTML gives the same block as one in Markdown.
  - Package `parse`: `Markdown` (the reader) and the HTML reader. Blocks: heading with its level 1 to 6, paragraph (also a quote and a code block), list (one line for each item, as the Word reader gives it), table (HTML with `th` for a header cell, which the chunker repeats in each part of a split table), figure (the alt text of a picture), caption, footnote. The number of a footnote in the text is in brackets, so that it is not read as a part of the number before it.
  - Left out: the metadata block at the start of a file (a YAML mapping between two lines of `---`, the rule of Docling), the address of a link, a comment, a script.
  - The parse worker selects the reader by the ending of the file. `documents.SaveFileBlocks` (before: `SaveWordBlocks`) saves the blocks and the name of the reader in one transaction, for a Word file and for a Markdown file.
  - The upload check and the unpack of an archive accept the two endings. Setting: `REJEA_MARKDOWN_MAX_BYTES` (16 MiB), because the file is in memory as a whole.
  - Failure: a file over the limit, a file in UTF-16, a file that is not UTF-8, and a file with a NUL character fail the document at attempt 1 with the reason and the correction. A file with no words fails in the index step, as each document with no text.
  - Proof, 2026-11-27, through the API of a test service on an empty database, with 11 uploads:
    - 4 real files (the README of goldmark, a page of the Hugo docs with the ending `.markdown`, this checklist with the ending `.MD`, and a sample judgment with each kind of element) are `indexed` with the reader `goldmark v1.8.7`. The reader took 3 to 11 ms for a file.
    - Words of the source that are in the blocks, with no link addresses: 100.0% (this checklist), 99.4% and 98.3%. The words that are not in the blocks are the names of the languages of code blocks, the metadata block and the names of HTML entities.
    - The sample judgment: 18 blocks of the 7 types, the heading path of each chunk is correct (for example "Mwangi v Republic > Background > Grounds of appeal"), a table in Markdown and a table in HTML give the same form, and the page of a result is `null`.
    - Search: 6 questions, one for each file. The first result is from the correct file for 6 of 6.
    - A zip with 2 Markdown files, a hidden file, a `__MACOSX` file and a `.txt` file, and a tar.gz with 2 Markdown files: only the Markdown files became documents. A file with the hash of an earlier upload is `duplicate`.
    - The 4 files with a fault are `failed` at attempt 1, each with its reason. A wrong value of the setting stops the service at its start with the name of the setting.
    - The same reader in 64 goroutines with the race detector of Go: no fault.
- [x] **44. Settings with no provider name** (added 2026-11-27, not in the ten steps of the plan)
  - Each model (embedding, reranker, chat) has four settings of the same form: `REJEA_<MODEL>_PROVIDER`, `REJEA_<MODEL>_MODEL`, `REJEA_<MODEL>_API_KEY` and `REJEA_<MODEL>_API_URL`. With no value, the model and the address are those of the provider.
  - Removed: `CLOUDFLARE_ACCOUNT_ID` (the account ID is in the address of Cloudflare) and the four addresses with a provider name. `REJEA_COHERE_MAX_TEXT_TOKENS` is now `REJEA_RERANK_MAX_TEXT_TOKENS`. A settings file with an old name stops the start with the correction for each line.
  - `REJEA_PARSER_URL` selects the parser with `make up` also: no value is the parser of the machine, an address is a parser on another machine.
  - `.env.example`: each of the 101 settings is a line with its default, in sections by purpose (models, parser, `make up`, `make serve`, speed, logs, limits). No setting is a comment.
  - Proof by runs on 2026-11-27: each default in the file is the default of the code (the file and an empty environment load the same settings). The three models answer at the start of the service with the new settings (Cloudflare, Cohere, Anthropic), and the reranker of Cloudflare also. Old names, no address, an address with no account and a wrong account ID each give their correction.

## Chunk and index (plan step 4)

- [x] **20. Chunker**
  - Split at headings. Split a long section at paragraphs, merge short ones, keep each chunk under 4,096 tokens. Start each chunk with the title and the heading path.
  - Logic only. It writes no rows, because a `chunks` row needs its embedding.
  - The method is the HybridChunker of Docling: each block gets the path of the headings above it, a block that is too large is split, and blocks that are next to each other in one section are put together up to the limit. A heading with no text below it is a chunk of its own (the Docling option `always_emit_headings`): 11 of 64 headings in the 12 documents of item 19.
  - The limit is for the full text of a chunk: the heading path, an empty line, the body. It is the setting `REJEA_CHUNK_MAX_TOKENS`. Cloudflare gives the model a context of 8,192 tokens.
  - Token count: the tokenizers library of Hugging Face (Rust) through the Go binding `daulet/tokenizers` 1.27.0, with the `tokenizer.json` of Qwen3-Embedding-0.6B. `make tokenizer-install` gets the two, each with its SHA-256 hash. The service is thus built with CGO. Measured: 690 texts, 0 differences from the Python library, 155,000 tokens in 118 ms. A count of characters is not safe: a table has 1.7 characters for each token and a paragraph has 4.1. The pure Go library `sugarme/tokenizer` cannot read the tokenizer of the model.
  - A table that is too large is split at its rows, as the table chunker of Unstructured does: each part starts with the header rows, rows that a cell of more than one row holds together stay in one part, and such rows that are too large for one part go one by one, each with a copy of the cell that covers it. The parser marks no header row, so the header rows are the first row and the rows that its cells cover. 2 tables of the 12 documents are larger than 4,096 tokens.
  - Other text that is too large is split by the recursive character splitter of LangChain (`langchaingo`): at empty lines, then lines, then spaces.
  - A PDF heading has no level, so the heading path of a PDF is the title and the nearest heading. Docling and Unstructured do the same by default. The chunker reads `blocks.level`, so a later source of levels needs no change of the chunker. The endpoint `/restructure-pages` of the parser is not used: it needs the full answer of the parser for each page, which the service does not keep.
  - Header, footer and page number blocks are in no chunk. Each other block with text is in a chunk: also footnotes and text in the margin, as in Docling.
  - Proof: the blocks of the 12 documents of item 19, at limits of 4,096, 512, 128 and 32 tokens, checked with the Python library: no chunk over the limit, each block in a chunk, each split table equal to the original cell by cell, the heading path and the pages of each chunk, and no two chunks of one section that fit in one. 48 runs, 0 faults. The small limits found two faults that other documents can cause at 4,096, and both are corrected: the header rows of a table were in no part when no row fits with them, and rows held together by one cell were cut as text.
  - Not proved: the library on Linux counts the same as on the Mac. CI builds the service with it, and item 22 runs it.

- [x] **21. Embed port, the Cloudflare adapter, the registry and the configurator**
  - One small interface for embeddings, and the adapter for Qwen3-Embedding-0.6B on Cloudflare Workers AI.
  - The registry (provider name to adapter) and the configurator (reads the config and connects them).
  - Retries, timeouts and the usage log are shared code, not adapter code.
  - Proof: a text comes back as 1,024 numbers.
  - Built: the port `internal/embed` (one method for documents, one for a search question, as the Embedder of LangChain), the adapter `internal/cloudflare`, the registry and the configurator in `internal/models`.
  - The new attempts are those of the library `hashicorp/go-retryablehttp`: for a request with no answer, status 429 and a status from 500, with the `Retry-After` header.
  - A search question goes with the instruction of the Qwen3 model card. A chunk goes as it is.
  - Cloudflare publishes no token limit for one request, and it refused some batches as "input too big". The client sends a refused batch again as two halves. This is our own choice: no Cloudflare document gives it.
  - The service sends one small request when it starts. No credentials: a warning, and `make up` says it also. A wrong token, account ID, model name or number of dimensions: the service stops and names the setting.
  - Proved by runs: one text gave 1 vector of 1,024 numbers with the length 1. The 80 chunks of the 12 test documents gave 80 vectors, each equal to the vector of the same chunk alone. Cloudflare refused 4 batches in that run, and the halves passed.
  - Proved by runs: 11 fault cases of the settings, and `make up` with good values, with no values and with a wrong token.
  - It writes no row: item 22 saves the vectors.

- [x] **22. Index worker: chunk and embed**
  - The parse worker adds the index job to the `index` queue.
  - The worker makes the chunks, embeds them in batches, and saves `chunks` with pages and block ids. The status becomes `indexed`.
  - Proof: each chunk of a document has text, pages and an embedding.
  - Built: the status `indexing` (our own addition to the statuses of the plan). The parse worker moves a parsed document to `indexing` and adds the index job in one transaction, as the file worker does for the parse (transactional enqueue of River).
  - The worker gets the embeddings with no open transaction. Then one transaction replaces the chunks of the document and sets `indexed`. A new attempt does all of the work again and leaves no part of an old attempt.
  - The vectors go to the database with `pgvector-go`, the Go library of the pgvector author. Migration 6 adds an index on `chunks.document_id`.
  - The service stops at its start when `REJEA_EMBED_DIMENSIONS` is not the number of dimensions of the column `chunks.embedding`.
  - Proved by a run through the API: 11 documents (10 PDF files of 57 pages, 1 Word file) went from upload to `indexed` in 11 minutes, most of it the parse. 77 chunks: each has text, blocks and a vector of 1,024 numbers with the length 1, and each chunk of a PDF has its pages. The index work of one document took 1 to 36 seconds.
  - Proved: 6 saved vectors are equal to a new embedding of their saved text (cosine 1.0000).
  - Proved: 11 failed documents, sent again with 4 index workers at the same time, were indexed in 42 seconds, with 77 chunks and no double rows.
  - Proved fault cases, each with its reason on the document: no credentials (then indexed after the credentials and a retry), no answer from Cloudflare, a PDF with no text. A stop of the service during the index work: the job ran again at the next start, 24 chunks, no double rows.
  - Not proved: a document of more than 13 pages, and more than 4 documents at the same time.
  - A document that was parsed before this item keeps the status `parsing`: item 23 gives the command for it.

- [x] **23. Chunk again without a new parse**
  - A command that makes the chunks of a document again from its saved blocks.
  - **Step check of the plan:** every chunk has text, pages and an embedding. Re-chunking runs without re-parsing.
  - The reason: a document gets its chunks and vectors one time. After a change of the chunk size or of the model, the saved chunks are old, and vectors from two models cannot be compared. The usual name is re-embedding (Qdrant and MongoDB Voyage AI documents).
  - Built: `rejea reindex --all` and `rejea reindex <document-id>...`, and `make reindex` (with `DOC=<id>` for one document). The command gives a parsed document the status `indexing` and a new index job in one transaction. The service does the work of item 22 with its own settings. For all documents, it works in groups of `REJEA_REINDEX_BATCH_SIZE`.
  - A document keeps its old chunks until the new chunks take their place in one transaction.
  - Proved by a run: with 512 tokens, 13 documents went from 83 to 224 chunks in 61 seconds. With the old setting again: 83 chunks. The blocks, the parse times of the pages and the log of the parser did not change: the parser did not run.
  - Step check, on the 224 chunks: each has text, blocks of its own document and a vector of 1,024 numbers, and each of the 211 chunks of a PDF has pages in the document.
  - Proved: a document that was parsed before item 22 got its chunks with `make reindex DOC=<id>`.
  - Proved fault cases, each with its message: no argument, a text that is not an ID, an ID of no document, a failed document (the message gives the retry request), a document with no complete parse, a duplicate, a document in the index work already, the service not started.
  - Not built: a change of the model with no time in which the search is wrong (the two-collection method of Qdrant). The search of a later item decides which chunks it reads during a reindex.

## Test set (plan step 5)

- [x] **24. Question file and its script** (needs point C)
  - 30 to 50 questions: fact lookup, reasoning across passages, summary across documents. Each has its expected answer and source page. A few have no answer in the documents.
  - **Step check of the plan:** a script can load them.
  - The technique: a question file with the correct answer and the correct pages for each question (the form of GraphRAG-Bench and of BEIR). The three kinds are levels 1 to 3 of GraphRAG-Bench. The questions with no answer are those of SQuAD 2.0: they look like the others, and the correct result is no answer.
  - Built: `testset/questions.json`, the loader `internal/testset`, the command `testset check <question file>` and `make testset-check` (also in `make check` and in CI).
  - The file: 13 public KNBS reports with 289 pages, one or more of each of the 10 report types of the collection (cpi, ppi, cipi, bop, labour, gdp_quarterly, gcp, lei, county_stats, poverty). Each document has its URL, its SHA-256 hash and its number of pages. One report is a scan, and one is an infographic.
  - The questions: 48. 23 `fact`, 12 `reasoning`, 7 `summary` (2 or 3 documents each), 6 `no_answer`. Each has its answer, its source pages and its evidence (the words of the page).
  - Proved by a run: the script loads the file and gives the counts. Each of the 90 evidence quotes is on the page that the question names: a script compared them with the text of the PDF files, and the 3 pages of the scan were read by eye. A search of all the documents found no text that answers one of the 6 `no_answer` questions.
  - Proved fault cases, each with its message: no file, JSON with a fault (with the line), a field with a wrong name, a wrong type, a page that the document does not have, a document that is not in the list, a summary with one document, a `no_answer` with an answer, an ID two times, no answer text, no evidence, a wrong hash, too few questions, a kind with no questions.
  - Not built: the runner that sends the questions to the service and gives the measures (recall at K, MRR, time, correct answers). The search does not exist yet: item 27 (search report) and item 30 (answer report) add the runner.

## Search (plan step 6)

- [x] **25. Search endpoint**
  - One query: keyword search and vector search, with a metadata filter. Merge the two lists by reciprocal rank fusion. Return the top chunks with document and page.
  - Proof: a known phrase returns its chunk first.
  - The technique: hybrid search with reciprocal rank fusion. The formula is that of the paper of Cormack, Clarke and Büttcher (2009): the score of a chunk is the sum of 1 / (k + rank), with k = 60. The SQL has the form of the hybrid search example of pgvector: two lists of candidates, each with a rank, and a full outer join.
  - Built: `POST /search` (`query`, `limit`, `filters.document_ids`, `filters.metadata`). The rules are in `documents.Search`, and the one SQL statement is in `database.SearchChunks`. A result has the chunk, its document, its pages, its heading path, its text, its blocks, its score and its rank in each of the two lists.
  - Facts from the documents of the two extensions. pg_textsearch: a query with parameters must name the BM25 index, and the score is negative (a chunk with no word of the question has 0: measured). pgvector: the HNSW index gives `hnsw.ef_search` rows at most (40 by default) and applies a filter after the scan, so the search sets `hnsw.ef_search` and `hnsw.iterative_scan` for its transaction.
  - My own choices: the search reads the documents with the status `indexed` or `indexing` (a document in the index work again keeps its old chunks; a failed document is not in the search). 50 candidates from each list. A question has 2,000 characters at most. With no embedding model, the search gives 503: it does not change to a keyword search with no word to the client.
  - Settings: `REJEA_SEARCH_LIMIT` (10), `REJEA_SEARCH_MAX_LIMIT` (50), `REJEA_SEARCH_CANDIDATES` (50), `REJEA_SEARCH_RANK_CONSTANT` (60), `REJEA_SEARCH_MAX_QUERY_LENGTH` (2000), `REJEA_SEARCH_MAX_DOCUMENTS` (1000), `REJEA_SEARCH_TIMEOUT` (20s).
  - Proved by a run, through the service of `make up`, on 17 documents with 330 chunks:
    - The 42 questions of the test set that have an answer: a source page is the first result for 36, in the first 5 for 41, and in the first 10 for all 42.
    - The proof of this item, with 60 phrases of 10 words, each from one chunk: the own chunk is the first result for 39, in the first 3 for 52, and in the first 10 for 58. In the keyword list alone it is first for 45, and in the vector list alone for 26. The merge puts a chunk that is high in the two lists above a chunk that is first in one list only: this is how reciprocal rank fusion works, and the reranker of item 26 is the part that corrects the order at the top.
    - The filters: one document, a metadata value that one document has, a value that no document has (no results).
    - The same results with a table scan and with index scans (`enable_seqscan` off).
    - Time: 0.6 to 1.4 seconds for a search (the medians of 3 runs of the 42 questions). The database part takes 24 ms. The rest is the call to the hosted embedding model for the vector of the question.
  - Proved fault cases, each with its message: an empty query, no body, JSON with a fault, an unknown field, a wrong type, a limit out of range, a query that is too long, a NUL character, an ID that is not a UUID, an ID of no document (no results), a wrong content type, a wrong method, words that no chunk has (vector results only), no embedding model (503, and the log gives the correction), a provider that gives no answer (503), a search that is too slow (503), and 5 wrong settings.
  - Proof of larger documents (open from item 22): 4 reports of 34, 47, 76 and 78 pages (235 pages) went through parse and index with no fault: 1,671 blocks and 247 chunks.
  - Not proved: the vector search through the HNSW index. With 330 chunks, PostgreSQL reads the table, which gives the exact result. The index path needs a larger collection: item 27 measures the time for each query.
  - Found, for item 27: a short report is one chunk (pages 1 to 3 of a 3-page report), so a result names a page range, not one page. The chunk size (`REJEA_CHUNK_MAX_TOKENS`, 4,096) is the setting to measure with the search report.

- [x] **26. Rerank port and the Cloudflare adapter**
  - One small interface for a reranker, the adapter for bge-reranker-base, and its use in search (a setting turns it on or off).
  - Proof: the order of the results changes with the reranker on.
  - The technique: two-stage search (retrieve, then rerank) with a cross-encoder model. The form of the port is that of the rerank APIs of Cohere and of Cloudflare: one question and a list of texts go in, one score for each text comes out.
  - Built: the port `internal/rerank` (one interface, the time limit, the check of the answer, the usage log), the adapter `cloudflare.NewReranker`, the registry line in `internal/models`, and the second stage in `documents.Search`. A request has the optional field `rerank`, and a result has `rerank_score`. The request code for Cloudflare is now one function for the two adapters (`internal/cloudflare/cloudflare.go`).
  - Facts from the pages of Cloudflare, each proved by a request: the request is `query` and `contexts[].text`; the answer is `response[].id` and `response[].score` (0 to 1), in the order from the best; an empty text gets status 400. The model reads 512 tokens (the model table of Cloudflare AI Search). Measured one time: a fact at the end of a text of 3,400 tokens got the same score as the text without it. The price is 0.003 US dollars for one million tokens. One request with 160 texts succeeded (3.2 seconds).
  - My own choices: the reranker is off by default (see the measurement). It reads the best 50 chunks of the merge. When the model gives no answer, the search gives 503, as for the embedding model: it does not change to the order of the merge with no word to the client. The start check sends one small request also when the default is off, because a request can turn the reranker on.
  - Settings: `REJEA_RERANK` (false), `REJEA_RERANK_CANDIDATES` (50), `REJEA_RERANK_PROVIDER` (cloudflare), `REJEA_RERANK_MODEL` (@cf/baai/bge-reranker-base), `REJEA_RERANK_TIMEOUT` (10s).
  - Measured before the code was written, with a script, on the 42 questions of the test set that have an answer (17 documents, 330 chunks, the 50 results of the search for each question). The number of questions for which a source page is the first result, in the first 5, in the first 10, and the mean reciprocal rank:
    - The search without the reranker: 36, 41, 42, MRR 0.912.
    - The reranker on the full text of each chunk (the model reads its first 512 tokens): 33, 39, 40, MRR 0.850.
    - The reranker on windows of 400 tokens of each chunk, with the best window as the score of the chunk (MaxP of Dai and Callan, 2019, which is also the procedure of Cohere Rerank for a long document): 34, 38, 41, MRR 0.863. It used 6 times the tokens.
    - Thus this model does not make the search better for these documents, with or without windows. The windows are not built.
  - Proved by a run, through the service of `make up`: the 42 questions without and with the reranker (`"rerank": false`, then `true`). The first result is a different chunk for 18 of the 42 questions with the reranker on: this is the proof of the item. The numbers are those of the script: 36, 41, 42 without, and 33, 39, 40 with. The median time of a search is 0.46 seconds without and 2.1 seconds with (one request of 50 chunks and 25,600 tokens).
  - Proved fault cases, each with its message: a wrong name of the model (the service stops and names the setting), a model of a different type, a wrong provider, `REJEA_RERANK` that is not true or false, too many candidates, 0 candidates, a time limit of 0, no credentials (a warning at the start), a model that gives no answer (a warning at the start, and 503 for a search that asks for the reranker, while a search with `"rerank": false` succeeds), a `rerank` field of the wrong type, and a filter that keeps no document (no results, and no request to the model).
  - Added after the measurement, on the user's word: a second provider, Cohere Rerank 5 (`internal/cohere`). The model of Cloudflare is from 2023, and Cloudflare has no other reranker (its model list, read on 2026-11-25).
    - Facts from the documents of Cohere: `POST /v2/rerank` with `model`, `query`, `documents`; the answer is `results[].index` and `results[].relevance_score` (0 to 1); the models `rerank-v5.0-pro` and `rerank-v5.0-fast` read 32,768 tokens; `max_tokens_per_doc` is 4,096 when the request gives none, so the adapter sends it (`REJEA_COHERE_MAX_TEXT_TOKENS`, 8192). The price is 0.02 (fast) and 0.05 (pro) US dollars for one million tokens. A trial key is free: 1,000 calls each month, 10 each minute.
    - Measured with a script on the same 42 questions (first result, first 5, first 10, MRR): `rerank-v5.0-pro` 40, 42, 42, 0.972; `rerank-v5.0-fast` 40, 42, 42, 0.970; the search without a reranker 36, 41, 42, 0.912. A call took 1.5 seconds (median) and 80,000 tokens on average.
    - Proved through the service of `make up` with the provider cohere: 40, 42, 42 for the 42 questions, and a median search time of 2.0 seconds. For one question, the 50 scores of the service are the same as the scores of a direct call to Cohere with the texts in the reverse order.
    - Proved fault cases, each with its message: no key (a warning at the start, 503 for a search that asks for the reranker, and a search with `"rerank": false` succeeds), a wrong key, a model that Cohere does not have, the model of Cloudflare with the provider cohere, a URL with no scheme, 0 tokens for a text, and the rate limit of the trial key (503, and the log has the reason of Cohere).
    - The shared code of the adapters (one JSON request, the reasons for a failure) is now in `internal/provider`. Each provider has a default model in the registry.
    - My own choices: the default provider stays cloudflare and the default stays off, because Cohere needs a second account and `make up` must work with no key. The README tells to use Cohere. Settings: `COHERE_API_KEY`, `REJEA_COHERE_API_URL`, `REJEA_COHERE_MAX_TEXT_TOKENS` (8192).
    - Not measured: `rerank-v5.0-pro` in the service (only by the script), and the limit of 1,000 calls each month of a trial key.
  - Found, for item 27: half of the chunks (166 of 330) have more than 450 tokens, so the reranker does not read the end of them. The report of item 27 must measure the reranker again for each chunk size: with chunks of 512 tokens or less, the model reads the full chunk.

- [x] **27. Search report**
  - **Step check of the plan:** the script reports, with and without the reranker, recall at K, MRR and the time for each query.
  - The technique: the search measures of trec_eval, the tool of NIST (read in its source code): success at K, recall at K, and reciprocal rank. A result is on a correct page when it is a chunk of a source document of the question and its pages have a source page.
  - Built: `make search-report`. The command `rejea search-report` runs in the container of the service, so each question goes through the search of the service with its true settings. It writes the report as a JSON file (`testset/reports/last.json`): the settings, the number of chunks, the measures of each run, and for each question the position of the first result on each correct page and the time. `testset compare` prints the report and its differences from the saved report, `testset/report.json`: each changed setting, each measure, and each question whose first correct result moved. `SAVE=1` saves the report as the new benchmark.
  - My own choices: the report is a file in the repository, and a later run is compared with it (the plan says "reports" only). The cutoffs are 1, 3, 5 and 10. A correct item is a page, not a document, so a question with pages in two documents has full recall only when the two are found. The 6 questions of the kind `no_answer` are in no measure, because no page is correct for them. `PAUSE` waits before each search with the reranker, for the rate limit of a trial key.
  - Proved by a run, through the service of `make up`: the report without the reranker gives the numbers of item 25 (36, 41, 42 of 42, MRR 0.912), and the report with Cohere gives the numbers of item 26 (40, 42, 42).
  - The chunk size, measured with the command (the reports are in `testset/reports/`; first result correct, in the first 5, in the first 10, of 42):
    - 4,096 tokens (320 chunks): 36, 41, 42 without the reranker; 40, 42, 42 with Cohere.
    - 2,048 tokens (420 chunks): 34, 39, 42; 40, 42, 42.
    - 1,024 tokens (662 chunks): 37, 40, 41; 39, 42, 42.
    - 512 tokens (1,439 chunks): 34, 39, 40; 40, 41, 41.
    - With the reranker, the sizes from 1,024 to 4,096 give the same: 42 questions cannot prove a difference of 1 question. Without the reranker, 4,096 has a correct page in the first 3 results for 41 questions and 1,024 for 38 or 39: a larger chunk is on more pages, so it is on a correct page more easily. At 512, one question loses its correct page from the first 10.
    - Pages for one chunk: 1.56 on average at 4,096 (40 chunks on 3 pages or more), and 1.25 at 1,024 (11 chunks on 3 pages or more). The median time of a search with the reranker is 2.2 seconds at 4,096 and 1.2 to 1.4 seconds at 1,024.
    - My own choice from these numbers: the default of `REJEA_CHUNK_MAX_TOKENS` is now 1,024, for the more exact source of a result and the shorter time with the reranker. Its cost: without a reranker, 4,096 finds a little more in the first 3 results. The claim of older sources that 512 is the best size is not true for these documents.
  - The saved benchmark, `testset/report.json` (chunks of 1,024 tokens, Cohere `rerank-v5.0-fast`): without the reranker 37, 38, 40, 41 of 42 (first result, first 3, first 5, first 10), MRR 0.904, median time 521 ms; with the reranker 39, 42, 42, 42, MRR 0.964, median time 1,435 ms. A run at the same settings 35 minutes earlier gave 39 in the first 3 without the reranker, not 38: two runs can differ by 1 question.
  - Proved fault cases, each with its message: no question file, a file that is not JSON, a wrong value of `--rerank`, a pause below zero, a document of the test set that is not in the service (with its URL), a document that is not indexed (with its status), no embedding model, the reranker of a provider with no key, a service that does not run, a report file that does not exist, a file that is not a report, and wrong arguments of `testset`.
  - Found and corrected during the measurement: a restart of the service during a long index job left the job with the state `running`, and its document stayed in the status `indexing`. Cause, proved by 3 runs: Docker on this machine kills a container 3 seconds after the stop signal (exit code 137), and the service gives its jobs 10 seconds to finish before it cancels and records them. River then starts the job again only after its rescue time (the job time limit plus 1 hour). Correction: `stop_grace_period: 30s` for the service in `compose.yaml`. Proved by a run: the service stopped in 10 seconds with exit code 0, River logged "Job stopped due to client shutdown; retrying", and the job was `available` again with no lost attempt.
  - Not measured: the questions of the kind `no_answer` (item 30 measures the answers), `rerank-v5.0-pro` at each chunk size, and the search with more documents than the 17 of this collection.

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

- [x] **32. Endpoint: pages of a document**
  - The size of each page, and a signed CloudFront URL for each page image. Config reads the CloudFront values.
  - Proof: a signed URL opens the image. The same URL without the signature is refused.
  - Built: `GET /documents/{id}/pages` with `limit` and `starting_after`, as the list of documents has. The answer has `pages` (number, width, height, `image_url`), `has_more` and `expires_at`. The settings are `REJEA_PAGES_LIMIT` (100), `REJEA_MAX_PAGES_LIMIT` (1,000), `REJEA_VIEW_URL_LIFE` (1 hour) and the three `REJEA_CLOUDFRONT_` values.
  - The technique, from the CloudFront documentation (read on 2026-11-25) and the source code of the AWS SDK for Go (`feature/cloudfront/sign` 1.12.12 of 2026-11-10): a signed URL with a custom policy that has a wildcard, which the documentation gives for more than one file. One signature is for the folder of the page images of one document, so a list of 1,000 pages costs one signature. The signature uses SHA-256 (`Hash-Algorithm=SHA256`), which CloudFront accepts in place of its default, SHA-1. Signed cookies, the other way of the documentation for more than one file, need the web app and the distribution on one site: no custom domain exists yet.
  - My own choices: with no CloudFront settings, the URL is a presigned URL of the bucket, so `make up` works with no AWS account. `compose.yaml` gives the service of `make up` no CloudFront, because its files are in the store of that file. The key is an RSA key of 2,048 bits only: CloudFront accepts an ECDSA key also, but no run proved one. The service proves the CloudFront settings at its start with one signed request for a file that does not exist: CloudFront examines the signature before it asks the bucket.
  - Proved by a run with the AWS bucket and the distribution (the service binary with the settings of `.env`, one page image of a test document put in the bucket, and removed after):
    - The signed URL opens the image: status 200, `image/png`, and the same bytes as the original.
    - The same URL without the signature: status 403 (`MissingKey`).
    - A changed signature: 403. The same signature for a different file of the document (`original.pdf`): 403. The signature without `Hash-Algorithm=SHA256`: 403, so SHA-256 is in use.
  - Proved by a run through the service of `make up` (the local store): the presigned URL opens the image (200, PNG), and the URL without the signature gets 403. Two lists with `limit=2` and `starting_after=2` give the 3 pages of a document. The list of the largest document (78 pages) takes 10 ms.
  - Proved fault cases, each with its message. Settings: only one of the three CloudFront values, a domain with `https://`, a key file that does not exist, a file that is not a key, an RSA key of 4,096 bits, an Ed25519 key, an ECDSA key, a different RSA key (CloudFront refuses the signature), a key pair ID that CloudFront does not know, a domain with no distribution, a URL life of 0, a URL life of 200 hours for a bucket URL, and a page limit above the largest limit. Requests: a document that does not exist (404), an ID that is not an ID (404), a limit that is not a number, a limit of 5,000, a `starting_after` below zero, an unknown parameter (400 each), and a Word document (an empty list).
  - Found, for item 33: a page image is a PNG of 1785 x 2526 pixels and 4.4 MB. A viewer that shows many pages loads much data. The distribution has no CORS headers: an `img` element needs none, and a canvas that reads the pixels needs them.
  - Not proved: a browser that shows the image (item 33), and the service of `make up` with CloudFront (its files are not in the AWS bucket).

- [ ] **33. Page viewer**
  - Shows the page image and draws a box on the blocks of the cited chunk. A toggle hides the boxes.
  - Loads the page sizes first, then only the pages in view and a few ahead.

- [ ] **34. Word viewer**
  - Shows the text of a Word document with the cited section highlighted.
  - **Step check of the plan:** a click on a citation opens the cited page with the correct blocks boxed.

## Types and properties (plan step 9)

- [x] **35. Chat port and the Claude adapter**
  - One small interface for a chat model, and the adapter for Claude.
  - Built: the port `internal/chat` (a question with instructions gets an answer: text, or JSON in the form of a JSON Schema), the adapter `internal/anthropic`, the entry in the registry of `internal/models`, and the start check in `rejea serve`. No part of the service asks the chat model a question yet: the classifier (item 37) and the extraction (item 40) are its first users.
  - Facts from the documents and the source code of the Go SDK of Anthropic (version 2.15.0 of 2026-11-24): the adapter uses the SDK, as the guide of Anthropic says, and not its own HTTP code. `output_config.format` with a JSON Schema gives an answer that is valid JSON of that form. A model can decline a request (`stop_reason` `refusal`). The request asks for the refusal fallback (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`): Anthropic then gives the request to a different Claude model in the same call. The instructions have a cache mark, so a second request with the same instructions pays less for them.
  - The default model is `claude-sonnet-5-5`, the newest Sonnet (your decision; 2 US dollars for one million input tokens, 10 for output): `REJEA_CHAT_MODEL` selects a different model. My own choices: the start check asks the Models API for the model, which costs nothing, and sends no question. The shared HTTP client of the models makes the new attempts, so the SDK makes none: one rule for each provider (`REJEA_MODEL_MAX_ATTEMPTS`). An answer has 16,000 tokens at most (`REJEA_CHAT_MAX_ANSWER_TOKENS`) and a request has 5 minutes (`REJEA_CHAT_TIMEOUT`).
  - The names of the key settings changed (your decision): each model has three settings of the same form, `REJEA_<MODEL>_PROVIDER`, `REJEA_<MODEL>_MODEL` and `REJEA_<MODEL>_API_KEY`, for the embedding model, the reranker and the chat model. `CLOUDFLARE_API_TOKEN` is now `REJEA_EMBED_API_KEY`, and `COHERE_API_KEY` is now `REJEA_RERANK_API_KEY`. `CLOUDFLARE_ACCOUNT_ID` keeps its name: it is not a key. No single standard exists: of 7 open source projects read on 2026-11-25, LightRAG, Onyx and RAGFlow use a generic name for each model, and AnythingLLM, R2R, Khoj and Aider use the name of the provider. With the generic name, the reranker of Cloudflare needs the token in `REJEA_RERANK_API_KEY` also.
  - Proved by a run with a real key (the code of the port and the adapter, through a temporary command): a question gets the text answer "Nairobi", and a question with a schema gets valid JSON of that form, with `claude-sonnet-5-5`, `claude-haiku-5-5` and `claude-opus-5-5`. Two questions with the same long instructions: the second one reads 16,810 of its 16,828 input tokens from the cache. The log has one line for each answer, with the model and the tokens.
  - Proved by a run through the service of `make up`, with the new names: the three models start ("chat model ready"), and a search with the reranker gives results with a score.
  - Proved fault cases, each with its message: a settings file with an old key name (the message gives the new name), a chat provider that is not in the registry, a wrong chat key (the service stops), an API address that gives no answer (a warning, the service starts), an API address that is not a URL, 0 answer tokens, a time limit of 0, no key for the embedding model, no key for the reranker, a reranker key of a different provider, a chat model name that does not exist, an answer limit of 1 token (the answer is not complete), and a schema that is not correct.
  - Found during the proof: a key that Anthropic no longer accepts gets status 401 with each request. The message of the service names the setting to correct.
  - Not proved: a request that a model declines, and the refusal fallback in work. No safe request causes a decline on purpose.

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

- CloudFront logs and a custom domain (AWS only).
- From the "Later" column of the plan: formats beyond PDF, Word and Markdown, spreadsheets, audio, video, roles and permissions.
