# go-rag

File-watched RAG service in Go — ingest local documents & images, embed with Jina, store in Cloudflare Vectorize + D1, and chat via Cloudflare Workers AI with retrieval-augmented generation.

Runs as both a terminal REPL and a web UI with SSE streaming, file uploads, and image captioning.

## Features

- **Auto-ingest** — watches `document/ingest/` and `document/images/` with `fsnotify`; debounced, chunked, embedded, and upserted
- **Smart chunking** — paragraph/sentence-aware splitting with overlap
- **Embeddings** — Jina API with `retrieval.passage` / `retrieval.query` task separation
- **Storage** — Cloudflare D1 for documents + Vectorize for vectors
- **Retrieval** — top-K cosine search with context formatting
- **Query rewriting** — conversational query rewrite before retrieval
- **Chat** — Cloudflare Workers AI via `goai`, SSE streaming on `/api/chat/stream`
- **Vision** — optional image description for search index
- **Web UI** — `GET /chat`, `POST /api/upload`, `POST /api/upload/image`, `POST /api/caption`, `GET /api/upload/events` (SSE), static `/images/*`
- **Prompt injection defense** — middleware on chat/upload routes

## Architecture

```
document/ingest/*.md|*.txt ──┐
document/images/* ───────────┤
                             ├─► ingest.Watch / WatchImages ─► chunk ─► Jina embed ─► D1 + Vectorize
                             │
user question ─► Rewriter ─► Retriever (embed query → Vectorize search → D1 fetch → formatContext) ─► ChatStream ─► SSE / REPL
```

**Prompt flow:** system prompt → history → rewrite query → retrieve context → inject as `contextPreamble + excerpts + --- Question --- + question` → stream.

Image excerpts are tagged `[image: /images/<file>]` and only rendered by the LLM when the user explicitly asks for something visual.

## Prerequisites

- Go 1.26.7
- Cloudflare account with **D1**, **Vectorize**, and **Workers AI** enabled
- [Jina AI](https://jina.ai/) API key for embeddings

## Quick Start

```bash
# 1. clone & configure
cp .env.example .env
# edit .env — see Configuration below

# 2. create Vectorize index (cosine, dimensions must match Jina model)
go run ./cmd/vectorize/create-index

# 3. run — REPL + watcher + optional web server
go run ./cmd/rag

# Prompt on startup:
#   Process existing files now? [y/N] (5s): y
```

Delete the index if you need to recreate it:

```bash
go run ./cmd/vectorize/delete-index
```

### Docker

```bash
# build
docker build -t go-rag .

# run — web UI on http://localhost:8080/chat
docker run --env-file .env -p 8080:8080 -v ./document:/app/document go-rag

# with custom address (Dockerfile defaults HTTP_ADDR=:8080)
docker run --env-file .env -p 3000:3000 -e HTTP_ADDR=:3000 -v ./document:/app/document go-rag
```

Mounting `./document` keeps `ingest` / `images` / `processed` on the host so file watching and uploads persist outside the container. `--env-file .env` supplies the Cloudflare/Jina credentials.

## Configuration

All config via env vars / `.env` loaded with `github.com/joho/godotenv`:

| Variable | Required | Default | Description |
|---|---|---|---|
| `CLOUDFLARE_ACCOUNT_ID` | yes | — | Cloudflare account ID |
| `CLOUDFLARE_API_TOKEN` | yes | — | API token with D1 + Vectorize access |
| `CLOUDFLARE_MODEL` | yes | — | Workers AI chat model (e.g. `@cf/meta/llama-3.1-8b-instruct`) |
| `CLOUDFLARE_D1_DATABASE_ID` | yes | — | D1 database ID |
| `CLOUDFLARE_VECTORIZE_INDEX` | yes | — | Vectorize index name |
| `CLOUDFLARE_VECTORIZE_DIMENSIONS` | yes | `1024` | Embedding dimensions — must match Jina model |
| `JINA_EMBEDDING_BASE_URL` | yes | `https://api.jina.ai/v1/embeddings` | Jina endpoint |
| `JINA_API_KEY` | yes | — | Jina API key |
| `JINA_EMBEDDING_MODEL` | yes | — | e.g. `jina-embeddings-v3` |
| `SYSTEM_PROMPT_FILE` | no | — | Path to system prompt (default `prompts/system-custom.md`) |
| `HTTP_ADDR` | no | — | If set (e.g. `:8080`), starts web server at `http://localhost:8080/chat` |
| `VISION_MODEL` | no | — | Workers AI vision model to enable `/api/caption` and image description |
| `INGEST_DIR` | no | `./document/ingest` | Source docs dir |
| `PROCESSED_DIR` | no | `./document/processed` | Chunk output dir |
| `IMAGES_DIR` | no | `./document/images` | Image source dir |

`.env` is gitignored.

## Project Structure

```
.
├── app/app.go                 # wiring: embedder, stores, retriever, watcher, web + REPL
├── cmd/
│   ├── rag/main.go            # entrypoint
│   └── vectorize/{create-index,delete-index}/main.go
├── chat/repl.go               # terminal REPL with spinner + streaming
├── cloudflare/client.go       # Cloudflare API client (D1 + Vectorize)
├── config/config.go           # env loading + defaults
├── document/
│   ├── ingest/                # drop .md/.txt here (gitignored except .gitkeep)
│   ├── images/                # drop images here
│   └── processed/             # generated chunks
├── ingest/
│   ├── ingest.go              # processContent, chunk, upsert, stale cleanup
│   ├── chunk.go               # size-aware chunker
│   ├── watcher.go             # file watcher for docs
│   └── image_watcher.go / image.go
├── llm/
│   ├── chat.go                # Cloudflare Workers AI chat (stream + non-stream)
│   ├── embed.go               # Jina embedder
│   └── vision.go              # image description via vision model
├── rag/
│   ├── retriever.go           # vector search → D1 fetch → context
│   ├── rewriter.go            # query rewrite
│   └── prompt.go              # contextPreamble + formatContext
├── store/
│   ├── document.go            # D1 document store
│   ├── vector.go              # Vectorize store
│   └── schema.sql             # documents table
├── web/
│   ├── server.go              # HTTP routes, SSE, uploads, image serving
│   ├── injection.go           # prompt injection defense
│   └── templates/             # chat UI
├── prompts/system-custom.md   # mythology expert system prompt (example)
└── web/templates/
```

## How It Works

1. **Ingest**: validates `.txt`/`.md`, chunks (default 1000 chars / 100 overlap), embeds via Jina, upserts to D1 and Vectorize, removes stale chunks, writes chunk files to `document/processed/<name>/chunk-*.md`.
2. **Watch**: `fsnotify` with 500ms debounce, handles create/write/remove/rename, recurses into subdirs.
3. **Images**: images need a `.description` sidecar or are auto-captioned via `VISION_MODEL`; stored as `type=image` chunks with `[image: ...]` metadata.
4. **D1 Schema**: `documents(id TEXT PK, content TEXT, metadata JSON)` + `json_extract(metadata,'$.source')` index.

## Usage

### REPL

```bash
go run ./cmd/rag
> What are leprechauns?
# streams answer with retrieval context; type q / quit / /exit to leave
```

### Web Chat

Set `HTTP_ADDR=:8080` in `.env`, then open `http://localhost:8080/chat`.

API:

| Method | Path | Description |
|---|---|---|
| `GET` | `/chat` | Chat page |
| `POST` | `/api/chat/stream` | SSE — `{"messages":[{"role":"user","content":"..."}]}` → `event: delta/done/error` |
| `POST` | `/api/upload` | multipart `file` (`.md`/`.txt`, 10 MiB max) → saves to ingest dir, triggers watcher |
| `POST` | `/api/upload/image` | multipart `image` + `description` |
| `POST` | `/api/caption` | multipart `image` → `{description}` (requires `VISION_MODEL`) |
| `GET` | `/api/upload/events?source=<file>` | SSE upload progress |
| `GET` | `/images/*` | Serve ingested images |

Uploads notify the UI via SSE.

### Supported Formats

- Documents: `.txt`, `.md`
- Images: `.png`, `.jpg`, `.jpeg`, `.webp`, `.gif`

## System Prompt

Default is `prompts/system-custom.md` — a mythology expert that refuses non-mythology questions and only renders `![alt](/images/...)` when the user asks to see something and an image excerpt matches. Swap via `SYSTEM_PROMPT_FILE`.

## Development

```bash
go vet ./...
go test ./...
go build -o bin/rag ./cmd/rag
```
