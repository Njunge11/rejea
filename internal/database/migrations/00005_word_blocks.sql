-- +goose Up

-- A Word file gives the level of each heading. The parser of a PDF gives
-- no level.
alter table blocks
  add column level int;  -- level of a heading, 1 is the top; null when not known

comment on column blocks.type is
  'title | heading | paragraph | table | list | figure | caption | formula | footnote | header | footer | page_number | aside';

-- +goose Down
comment on column blocks.type is
  'title | heading | paragraph | table | figure | caption | formula | footnote | header | footer | page_number | aside';
alter table blocks drop column level;
