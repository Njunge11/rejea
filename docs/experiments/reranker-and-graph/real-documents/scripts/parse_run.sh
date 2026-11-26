#!/bin/bash
# Parse and index a set of PDFs through a Rejea test service, with the parser at a given address. The database and the service stay for the search runs.
# Use: parse_run.sh <database> <parser URL as the container sees it> <pdf dir> <names.json> <upload.py> <ids.json>
# Settings (from the speed experiment): 16 file workers, 8 parse workers, 8 index workers.
set -u
dbname=$1; parser=$2; pdfs=$3; names=$4; upload=$5; ids=$6
cd "$(dirname "$0")/../../../../.." || exit 1      # the root of the repository
sql() { docker compose exec -T postgres psql -U rejea -d "$1" -X -At -F ' ' -c "$2" < /dev/null; }
db=postgres://rejea:rejea@postgres:5432/$dbname
docker rm -f rejea-e2e > /dev/null 2>&1
if [ "$(sql postgres "select count(*) from pg_database where datname='$dbname'")" = "0" ]; then
  sql postgres "create database $dbname" > /dev/null
  docker compose run --rm --no-deps -e DATABASE_URL=$db migrate > /dev/null 2>&1 < /dev/null || { echo "FAULT: migrate failed"; exit 1; }
fi
docker compose run -d --no-deps --name rejea-e2e -p 127.0.0.1:8082:8080 -e DATABASE_URL=$db \
  -e REJEA_FILE_WORKERS=16 -e REJEA_PARSE_WORKERS=8 -e REJEA_INDEX_WORKERS=8 -e REJEA_PARSER_URL="$parser" rejea > /dev/null 2>&1 < /dev/null
for i in $(seq 1 40); do [ "$(docker inspect rejea-e2e --format '{{.State.Health.Status}}' 2>/dev/null)" = "healthy" ] && break; sleep 2; done
[ "$(docker inspect rejea-e2e --format '{{.State.Health.Status}}' 2>/dev/null)" = "healthy" ] || { echo "FAULT: the test service is not healthy"; docker logs --tail 5 rejea-e2e; exit 1; }
total=$(python3 -I -c "import json,sys; print(len(json.load(open(sys.argv[1]))))" "$names")
python3 -I "$upload" http://127.0.0.1:8082 "$pdfs" "$names" "$ids" > /dev/null || { echo "FAULT: upload failed"; exit 1; }
start=$(date +%s); last=0
while true; do
  read -r indexed failed pages_done pages_all <<< "$(sql "$dbname" "select count(*) filter (where status='indexed'), count(*) filter (where status='failed'), (select count(*) from pages where parsed_at is not null), (select coalesce(sum(page_count),0) from documents) from documents")"
  now=$(( $(date +%s) - start ))
  [ $(( ${indexed:-0} + ${failed:-0} )) -ge "$total" ] && break
  [ $(( now - last )) -ge 120 ] && { echo "  ${now}s: pages parsed $pages_done of $pages_all | documents indexed $indexed, failed $failed, of $total"; last=$now; }
  [ "$now" -gt "${MAX_SECONDS:-1500}" ] && { echo "FAULT: no end after ${MAX_SECONDS:-1500} s (indexed $indexed, failed $failed, of $total)"; break; }
  sleep 3
done
sql "$dbname" "select json_build_object('documents', (select count(*) from documents), 'indexed', (select count(*) from documents where status='indexed'), 'failed', (select count(*) from documents where status='failed'), 'pages', (select count(*) from pages where parsed_at is not null), 'parse_seconds', round(extract(epoch from (select max(parsed_at) from pages) - (select min(created_at) from documents))::numeric, 1), 'all_seconds', $(( $(date +%s) - start )), 'blocks', (select count(*) from blocks), 'table_blocks', (select count(*) from blocks where type ilike '%table%'), 'chunks', (select count(*) from chunks), 'jobs', (select json_object_agg(k, n) from (select kind || ' ' || state as k, count(*) n from river_job where kind in ('file','parse','index') group by 1) s), 'errors', (select coalesce(json_agg(left(error, 160)), '[]'::json) from documents where status='failed'))"
