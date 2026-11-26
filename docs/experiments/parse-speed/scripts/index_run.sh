#!/bin/bash
# One run of the index job: make the chunks and the vectors of each parsed document again, with N index workers, and measure the time.
# Use: index_run.sh <label> <index workers> <results.jsonl>
# The run works on a copy of the main database (rejea_speed). The main database does not change. The parser does not run.
set -u
label=$1; workers=$2; out=$3
cd "$(dirname "$0")/../../../.." || exit 1
sql() { docker compose exec -T postgres psql -U rejea -d "$1" -X -At -F ' ' -c "$2"; }
db=postgres://rejea:rejea@postgres:5432/rejea_speed
docker rm -f rejea-speed > /dev/null 2>&1
sql postgres "drop database if exists rejea_speed" > /dev/null
sql postgres "create database rejea_speed template rejea" > /dev/null || { echo "FAULT: no copy of the main database (does a service still use it?)"; exit 1; }
sql rejea_speed "delete from river_job" > /dev/null
# The jobs go into the queue while no service runs, so that all of them wait when the service starts.
docker compose run --rm --no-deps -e DATABASE_URL=$db rejea reindex --all 2>&1 | grep -m1 "Documents in the index queue" || { echo "FAULT: reindex put no document in the queue"; exit 1; }
total=$(sql rejea_speed "select count(*) from documents where status='indexing'")
failed_before=$(sql rejea_speed "select count(*) from documents where status='failed'")
docker compose run -d --no-deps --name rejea-speed -e DATABASE_URL=$db -e REJEA_INDEX_WORKERS="$workers" rejea > /dev/null 2>&1
start=$(date +%s)
while true; do
  left=$(sql rejea_speed "select count(*) from documents where status='indexing'")
  [ "${left:-1}" = "0" ] && break
  [ $(( $(date +%s) - start )) -gt 900 ] && { echo "FAULT: no end after 15 minutes ($left documents left)"; break; }
  sleep 2
done
sleep 2
sql rejea_speed "select json_build_object('label', '$label', 'index_workers', $workers, 'documents', $total, 'indexed', (select count(*) from documents where status='indexed'), 'failed', (select count(*) from documents where status='failed') - $failed_before, 'chunks', (select count(*) from chunks), 'index_seconds', (select round(extract(epoch from max(finalized_at) - min(attempted_at))::numeric, 1) from river_job where kind='index'), 'job_seconds_median', (select round((percentile_cont(0.5) within group (order by extract(epoch from finalized_at - attempted_at)))::numeric, 2) from river_job where kind='index'), 'job_seconds_max', (select round(max(extract(epoch from finalized_at - attempted_at))::numeric, 2) from river_job where kind='index'), 'jobs_by_state', (select json_object_agg(state, n) from (select state::text, count(*) n from river_job where kind='index' group by 1) s), 'attempts_max', (select max(attempt) from river_job where kind='index'))" 2>&1 | tee -a "$out"
docker rm -f rejea-speed > /dev/null 2>&1
