-- +goose Up
create extension if not exists vector;
create extension if not exists pg_textsearch;

create table doc_types (
  id              text primary key,   -- 'sop', 'guide', 'technical', 'other'
  description     text not null,      -- read by the classifier
  property_schema jsonb not null      -- JSON Schema for this type's properties
);

-- documents.doc_type has the default 'other', so this row must exist before
-- the first document.
insert into doc_types (id, description, property_schema)
values ('other', 'A document that matches no other type', '{}');

create table documents (
  id          uuid primary key,
  uploaded_by uuid not null,
  filename    text not null,
  file_hash   text unique,            -- set by the workflow; duplicate detection
  storage_key text not null,          -- original PDF or Word file
  title       text,
  metadata    jsonb not null default '{}',  -- typed in at upload, department included
  doc_type    text not null default 'other' references doc_types,
  properties  jsonb not null default '{}',  -- filled by the classifier
  status      text not null,          -- uploading | uploaded | parsing | indexed | failed | duplicate
  error       text,
  parser      text,                   -- name and version, for re-runs
  page_count  int,
  created_at  timestamptz not null default now()
);

create table pages (
  document_id uuid references documents on delete cascade,
  page_number int,
  image_key   text not null,          -- rendered page image
  width       int not null,           -- pixels; the viewer lays out pages before images load
  height      int not null,
  primary key (document_id, page_number)
);

create table blocks (
  id          uuid primary key,
  document_id uuid not null references documents on delete cascade,
  page_number int,                    -- null for Word files
  seq         int not null,           -- reading order
  type        text not null,          -- heading | paragraph | table | list | figure
  bbox        real[],                 -- left, top, right, bottom as 0-1 fractions; null for Word files
  text        text not null
);

create table chunks (
  id           uuid primary key,
  document_id  uuid not null references documents on delete cascade,
  heading_path text[] not null,
  page_start   int,                   -- null for Word files
  page_end     int,
  block_ids    uuid[] not null,       -- blocks to highlight when cited
  text         text not null,         -- title > heading path, then section text
  embedding    vector(1024) not null  -- size depends on the embedding model
);
create index on chunks using bm25 (text) with (text_config='english');
create index on chunks using hnsw (embedding vector_cosine_ops);

-- +goose Down
drop table chunks;
drop table blocks;
drop table pages;
drop table documents;
drop table doc_types;
