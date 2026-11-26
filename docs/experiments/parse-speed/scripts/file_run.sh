#!/bin/bash
# One run of the file job (download the file from the file store, hash it, check for a duplicate) with N file workers.
# Use: file_run.sh <label> <file workers> <pdf dir> <names.json> <results.jsonl>
# The run uses a new, empty database (rejea_speed) and a parser address that does not answer: only the file job is measured.
# After the run, the script removes the files of the run from the file store.
set -u
label=$1; workers=$2; pdfs=$3; names=$4; out=$5
here=$(cd "$(dirname "$0")" && pwd)
cd "$here/../../../.." || exit 1
sql() { docker compose exec -T postgres psql -U rejea -d "$1" -X -At -F ' ' -c "$2"; }
db=postgres://rejea:rejea@postgres:5432/rejea_speed
docker rm -f rejea-speed > /dev/null 2>&1
sql postgres "drop database if exists rejea_speed" > /dev/null 2>&1; sql postgres "create database rejea_speed" > /dev/null
docker compose run --rm --no-deps -e DATABASE_URL=$db migrate > /dev/null 2>&1 || { echo "FAULT: migrate failed"; exit 1; }
docker compose run -d --no-deps --name rejea-speed -p 127.0.0.1:8082:8080 -e DATABASE_URL=$db -e REJEA_FILE_WORKERS="$workers" -e REJEA_PARSER_URL=http://127.0.0.1:9 rejea > /dev/null 2>&1
for i in $(seq 1 40); do [ "$(docker inspect rejea-speed --format '{{.State.Health.Status}}' 2>/dev/null)" = "healthy" ] && break; sleep 2; done
total=$(python3 -I -c "import json,sys; print(len(json.load(open(sys.argv[1]))))" "$names")
ids=$(mktemp)
python3 -I "$here/file_upload.py" http://127.0.0.1:8082 "$pdfs" "$names" "$ids" > /dev/null || echo "FAULT: upload failed"
start=$(date +%s)
while true; do
  n=$(sql rejea_speed "select count(*) from river_job where kind='file' and finalized_at is not null")
  [ "${n:-0}" -ge "$total" ] && break
  [ $(( $(date +%s) - start )) -gt 300 ] && { echo "FAULT: no end after 5 minutes ($n of $total file jobs)"; break; }
  sleep 1
done
docker stop -t 2 rejea-speed > /dev/null 2>&1
sql rejea_speed "select json_build_object('label', '$label', 'file_workers', $workers, 'documents', (select count(*) from documents), 'file_seconds', (select round(extract(epoch from max(finalized_at) - min(attempted_at))::numeric, 2) from river_job where kind='file'), 'job_seconds_median', (select round((percentile_cont(0.5) within group (order by extract(epoch from finalized_at - attempted_at)))::numeric, 3) from river_job where kind='file'), 'job_seconds_max', (select round(max(extract(epoch from finalized_at - attempted_at))::numeric, 3) from river_job where kind='file'), 'jobs_by_state', (select json_object_agg(state, n) from (select state::text, count(*) n from river_job where kind='file' group by 1) s), 'duplicates', (select count(*) from documents where status='duplicate'))" 2>&1 | tee -a "$out"
docker rm -f rejea-speed > /dev/null 2>&1
# The ids go to one shell in the storage container. (A docker command inside a "while read" loop takes the rest of the list.)
docker compose exec -T storage sh -c 'n=0; while read -r id; do [ -n "$id" ] && curl -s -o /dev/null -X DELETE "http://localhost:8888/buckets/rejea/documents/$id?recursive=true" && n=$((n + 1)); done; echo "folders removed from the file store: $n"' < "$ids"
rm -f "$ids"; sql postgres "drop database if exists rejea_speed" > /dev/null
