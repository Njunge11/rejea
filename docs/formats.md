# File formats, chunks and the index

What Rejea reads from each file format, and how it makes the chunks.

## PDF

The parser turns a page image into blocks: each with its box, its type and its text. It is PaddleOCR-VL-1.6, and it runs on Apple Silicon only: the vision model runs on the GPU through MLX.

On an M2 Pro with 16 GB, a page takes 3 to 40 seconds and the parser uses 3 GB of memory.

The service sends each page of a PDF to the parser. While the parser does not run, a PDF waits and the service tries again.

## Word

The service reads `.docx` and `.doc` files with the command line program of [office_oxide](https://github.com/yfedoseev/office_oxide), one process for each file. The image of the service has the program.

## Markdown

The service reads `.md` and `.markdown` files with [goldmark](https://github.com/yuin/goldmark), the Markdown library of Hugo. It needs no parser and no other program: a Markdown file goes from the upload to the chunks in milliseconds.

| In the file | Block |
| --- | --- |
| A heading (`#` to `######`, or a line with `===` or `---` below it) | A heading with its level, 1 to 6. The headings make the heading path of each chunk. |
| A paragraph, a quote, a code block | A paragraph |
| A list, also with tasks (`- [x]`) | One list, with one line for each item |
| A table, in Markdown or in HTML | A table |
| A picture | Its description (the alt text) |
| A footnote | A footnote. Its number in the text is in brackets: `[1]`. |

- The Markdown is [CommonMark](https://commonmark.org) with the additions of GitHub (tables, struck text, task lists) and footnotes.
- The reader leaves out the metadata block at the start of a file (YAML between two lines of `---`), the address of a link, a comment, and a script.
- The file must be text in UTF-8, with `REJEA_MARKDOWN_MAX_BYTES` bytes or less (16 MiB by default). A file with a fault fails at once with the reason.
- A Markdown file has no pages: a search result gives its heading path, and `null` for its pages.

The method is that of [Unstructured](https://github.com/Unstructured-IO/unstructured/blob/main/unstructured/partition/md.py), which [Docling](https://github.com/docling-project/docling/blob/main/docling/backend/md_backend.py) also uses for a file with HTML in it: the library turns the Markdown into HTML, and one HTML reader makes the blocks.

## Chunks

The chunker cuts the blocks of a document into chunks at its headings. Each chunk starts with its heading path, and has `REJEA_CHUNK_MAX_TOKENS` tokens or less (1,024 by default). A table that is larger is split at its rows, and each part starts with the header rows.

The default comes from the [search report](search-quality.md#search-report): with the reranker, the search finds the same from 1,024 to 4,096 tokens, and a chunk of 1,024 tokens is on fewer pages, so a result names its source more exactly.

The limit is in tokens of the embedding model, Qwen3-Embedding-0.6B. The service counts them with the [tokenizers](https://github.com/huggingface/tokenizers) library of Hugging Face, through [its Go binding](https://github.com/daulet/tokenizers). The image of the service has the library and the tokenizer of the model.

## Index

After the parse, a document has the status `indexing`. The index worker cuts its blocks into chunks, gets the vector of each chunk from the embedding model, and saves the chunks in one transaction. The document then has the status `indexed`. Each chunk has its heading path, its text, its pages, its blocks and its vector.

A fault that a new attempt can correct (the provider gives no answer) makes the job run again after a wait. After `REJEA_JOB_MAX_ATTEMPTS` attempts, the document has the status `failed` and the reason. `POST /documents/{id}/retry` starts a failed document again: it skips the parse that is complete. A document with no text fails at once.

`REJEA_INDEX_WORKERS` is the number of documents in the index work at the same time.

### New chunks for the documents in the database

A document gets its chunks and its vectors one time, with the chunk size and the embedding model of that day. After a change of `REJEA_CHUNK_MAX_TOKENS` or of the model, the documents in the database still have the old chunks. Vectors from two models cannot be compared, so after a change of the model the search cannot find the old documents correctly.

`make reindex` gives each parsed document new chunks and new vectors. It uses the blocks that the parse saved: the parser does not run.

```sh
# 1. Change the setting in .env, then start the service again with it.
make up
# 2. Put each parsed document in the index queue again.
make reindex
# One document:
make reindex DOC=01a20e0e-81f5-7043-922b-4ed3749f5436
```

The command adds the jobs, and the service does the work. A document has the status `indexing` until its new chunks are saved, and it keeps its old chunks until then. The command refuses a document with no complete parse, and it says why. Without Docker, the command is `go run ./cmd/rejea reindex --all`.

A model with a different number of dimensions also needs a migration of the column `chunks.embedding`.
