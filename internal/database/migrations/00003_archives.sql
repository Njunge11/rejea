-- +goose Up
-- An archive (zip or tar) gets the status 'unpacked'. Each file from the
-- archive is its own documents row that points to the archive.
alter table documents
  add column parent_id uuid references documents;  -- the archive that held this file; null for a direct upload

-- The status column has no constraint, so the list of statuses is a comment
-- in the database.
comment on column documents.status is
  'uploading | uploaded | unpacked | parsing | indexed | failed | duplicate';

-- +goose Down
comment on column documents.status is null;
alter table documents drop column parent_id;
