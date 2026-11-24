# syntax=docker/dockerfile:1

# The image of the Rejea service. The first stage builds the service. The
# second stage is the image that runs: it has no compiler.
#
# Each base image is a Docker Official Image, with its version and its
# digest. For a new version, change the two: "docker buildx imagetools
# inspect <image>:<tag>" gives the digest.

FROM golang:1.26-trixie@sha256:f424dcd3918d6bb63cb7e23b049bdd468c1a2f65d1a42d1215bbab065f9704f8 AS build
WORKDIR /src

# The tokenizers library, the tokenizer of the embedding model and the Word
# reader. They change less often than the code, so they come first.
COPY scripts/install-tokenizer scripts/install-word-reader scripts/
RUN scripts/install-tokenizer && scripts/install-word-reader

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd cmd
COPY internal internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_LDFLAGS=-L/src/lib go build -trimpath -o /out/rejea ./cmd/rejea

FROM debian:trixie-slim@sha256:8aa5a7bf5a1955bddbe7d74fcdd0b91ab30e74362a8b73af672b0481b35b44a0

# The certificates are for HTTPS requests to other services.
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# The service runs as a user that is not root.
RUN groupadd --system --gid 10001 rejea \
    && useradd --system --no-log-init --uid 10001 --gid rejea rejea

COPY --from=build /out/rejea /src/bin/office-oxide /usr/local/bin/
COPY --from=build /src/models/qwen3-embedding-0.6b/tokenizer.json /usr/local/share/rejea/tokenizer.json

# The HTTP server listens on each address of the container. The port of
# the machine that reaches it is in compose.yaml.
ENV REJEA_HTTP_ADDR=0.0.0.0:8080 \
    REJEA_WORD_READER_PATH=/usr/local/bin/office-oxide \
    REJEA_TOKENIZER_PATH=/usr/local/share/rejea/tokenizer.json

USER rejea
EXPOSE 8080
ENTRYPOINT ["rejea"]
CMD ["serve"]
