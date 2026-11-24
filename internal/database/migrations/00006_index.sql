-- +goose Up
-- A document has the status 'indexing' from the end of its parse until its
-- chunks and their embeddings are in the database.
comment on column documents.status is
  'uploading | uploaded | unpacked | parsing | indexing | indexed | failed | duplicate';

-- The chunks of one document: a new index run replaces them, and the
-- removal of a document removes them.
create index chunks_document on chunks (document_id);

-- +goose Down
drop index chunks_document;
comment on column documents.status is
  'uploading | uploaded | unpacked | parsing | indexed | failed | duplicate';
