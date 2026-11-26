#!/bin/bash
# One speed run: parse a fixed set of PDFs through a Rejea service with N parse workers, and measure the time.
# Use: speed_run.sh <label> <parse workers> <parser URL as the container sees it> <pdf dir> <names.json> <upload.py> <results.jsonl>
# The run uses a new, empty database (rejea_speed), so that no file is a duplicate of an earlier run.
set -u
label=$1; workers=$2; parser=$3; pdfs=$4; names=$5; upload=$6; out=$7
cd "$(dirname "$0")/../../../.." || exit 1
sql() { docker compose exec -T postgres psql -U rejea -d "$1" -X -At -F ' ' -c "$2"; }
docker rm -f rejea-speed > /dev/null 2>&1
sql rejea "drop database if exists rejea_speed" > /dev/null; sql rejea "create database rejea_speed" > /dev/null
docker compose run --rm --no-deps -e DATABASE_URL=postgres://rejea:rejea@postgres:5432/rejea_speed migrate > /dev/null 2>&1 || { echo "FAULT: migrate failed"; exit 1; }
docker compose run -d --no-deps --name rejea-speed -p 127.0.0.1:8082:8080 \
  -e DATABASE_URL=postgres://rejea:rejea@postgres:5432/rejea_speed -e REJEA_PARSE_WORKERS="$workers" -e REJEA_PARSER_URL="$parser" rejea > /dev/null 2>&1
for i in $(seq 1 40); do [ "$(docker inspect rejea-speed --format '{{.State.Health.Status}}' 2>/dev/null)" = "healthy" ] && break; sleep 2; done
[ "$(docker inspect rejea-speed --format '{{.State.Health.Status}}' 2>/dev/null)" = "healthy" ] || { echo "FAULT: the test service is not healthy"; docker logs --tail 5 rejea-speed; exit 1; }
total=$(python3 -I -c "import json,sys; print(len(json.load(open(sys.argv[1]))))" "$names")
ids=$(mktemp); rm -f "$ids"
python3 -I "$upload" http://127.0.0.1:8082 "$pdfs" "$names" "$ids" > /dev/null || { echo "FAULT: upload failed"; exit 1; }
start=$(date +%s)
while true; do
  read -r done_docs failed pages_done pages_all <<< "$(sql rejea_speed "select count(*) filter (where status='indexed'), count(*) filter (where status='failed'), (select count(*) from pages where parsed_at is not null), (select coalesce(sum(page_count),0) from documents) from documents")"
  [ $(( ${done_docs:-0} + ${failed:-0} )) -ge "$total" ] && break
  [ $(( $(date +%s) - start )) -gt 2400 ] && { echo "FAULT: no end after 40 minutes"; break; }
  sleep 5
done
sql rejea_speed "select json_build_object('label', '$label', 'parse_workers', $workers, 'documents', (select count(*) from documents), 'indexed', (select count(*) from documents where status='indexed'), 'failed', (select count(*) from documents where status='failed'), 'pages', (select count(*) from pages where parsed_at is not null), 'parse_seconds', round(extract(epoch from (select max(parsed_at) from pages) - (select min(created_at) from documents))::numeric, 1), 'seconds_per_page', round((extract(epoch from (select max(parsed_at) from pages) - (select min(created_at) from documents)) / nullif((select count(*) from pages where parsed_at is not null), 0))::numeric, 2), 'page_seconds_median', (select round((percentile_cont(0.5) within group (order by s))::numeric, 1) from (select extract(epoch from parsed_at - lag(parsed_at) over (partition by document_id order by parsed_at)) s from pages) g where s is not null), 'page_seconds_p90', (select round((percentile_cont(0.9) within group (order by s))::numeric, 1) from (select extract(epoch from parsed_at - lag(parsed_at) over (partition by document_id order by parsed_at)) s from pages) g where s is not null), 'blocks', (select count(*) from blocks), 'table_blocks', (select count(*) from blocks where type ilike '%table%'), 'words', (select coalesce(sum(array_length(regexp_split_to_array(trim(text), '\s+'), 1)), 0) from blocks), 'errors', (select coalesce(json_agg(left(error, 160)), '[]'::json) from documents where status='failed'))" | tee -a "$out"
docker rm -f rejea-speed > /dev/null 2>&1
