# syntax=docker/dockerfile:1

# PostgreSQL 17 with pgvector (vector index) and pg_textsearch (BM25 index).
# pgvector gives the base image. pg_textsearch gives pre-built packages for
# Linux only, on amd64 and arm64, so the package of the build platform is
# added here.
FROM pgvector/pgvector:0.8.7-pg17-trixie

ARG TARGETARCH
ARG PG_TEXTSEARCH_VERSION=1.5.1
ARG PG_TEXTSEARCH_SHA256_AMD64=ed4410e8b6879a8971cd87fd03853f182e47b85b199c9bfcd9b34f159caba9fb
ARG PG_TEXTSEARCH_SHA256_ARM64=e65f953e99f47c05dde88a031884cf36ea269bcb62377f554b76ddb4d13fdc3e

ADD https://github.com/timescale/pg_textsearch/releases/download/v${PG_TEXTSEARCH_VERSION}/pg-textsearch-v${PG_TEXTSEARCH_VERSION}-pg17-${TARGETARCH}.zip /tmp/pg_textsearch.zip

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) sha256="${PG_TEXTSEARCH_SHA256_AMD64}" ;; \
      arm64) sha256="${PG_TEXTSEARCH_SHA256_ARM64}" ;; \
      *) echo "pg_textsearch has no package for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    echo "${sha256}  /tmp/pg_textsearch.zip" | sha256sum -c -; \
    apt-get update; \
    apt-get install -y --no-install-recommends unzip; \
    unzip /tmp/pg_textsearch.zip -d /tmp/pg_textsearch; \
    apt-get install -y --no-install-recommends /tmp/pg_textsearch/*.deb; \
    apt-get purge -y --auto-remove unzip; \
    rm -rf /tmp/pg_textsearch /tmp/pg_textsearch.zip /var/lib/apt/lists/*

# pg_textsearch must be loaded when the server starts.
CMD ["postgres", "-c", "shared_preload_libraries=pg_textsearch"]
