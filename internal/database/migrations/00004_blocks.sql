-- +goose Up

-- The parser reads one page at a time. A page with no parsed_at is a page
-- that the parser must read. A blank page is parsed and has no block.
alter table pages
  add column parsed_at timestamptz;

comment on column blocks.type is
  'title | heading | paragraph | table | figure | caption | formula | footnote | header | footer | page_number | aside';

comment on column blocks.seq is
  'reading order: in the page for a PDF, in the document for a Word file';

-- One block for each place in the reading order. A Word file has no page
-- number, so two nulls count as the same value. The index also gives the
-- blocks of a document in reading order.
create unique index blocks_reading_order
  on blocks (document_id, page_number, seq) nulls not distinct;

-- +goose Down
drop index blocks_reading_order;
comment on column blocks.seq is null;
comment on column blocks.type is null;
alter table pages drop column parsed_at;
