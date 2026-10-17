# curly-engine

Style Engine: extract a reusable design system (`components.xml`) from PPTX/HTML presentations and generate new decks in that style with minimal LLM cost.

Styling is extracted **deterministically** (no LLM): theme colors, fonts, type scale, components, table styles, slide layouts with named slots, and recurring placements (logo, page number, footer). See [docs/DESIGN.md](docs/DESIGN.md).

## Requirements

- Go 1.23+
- Chrome/Chromium for HTML decks (auto-detected, or set `CHROME_PATH`). PPTX extraction needs nothing else.

## Usage

```bash
go build -o bin/styleengine ./cmd/styleengine

# One-off extraction
./bin/styleengine extract testdata/acme.pptx -o components.xml
./bin/styleengine extract testdata/nimbus.html -o components.xml

# Server: SQLite + local blobs under ./data
./bin/styleengine serve -addr :8080 -data ./data
```

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/uploads` | multipart `file` (+ optional `name`) → `202 {uploadId, job}`; identical files reuse the same job |
| `GET` | `/api/jobs/{id}` | job status/result |
| `GET` | `/api/jobs/{id}/events` | SSE progress (`stage`, `done`, `error`) |
| `GET` | `/api/profiles` | list profiles |
| `GET` | `/api/profiles/{id}?version=N` | profile metadata + parsed profile JSON |
| `GET` | `/api/profiles/{id}/components.xml?version=N` | raw `components.xml` |
| `PUT` | `/api/profiles/{id}/components.xml` | save an edited profile as a new version (validated) |
| `GET` | `/api/assets/{sha256}` | extracted asset (logo, images) |

```bash
curl -F file=@testdata/acme.pptx -F name=Acme localhost:8080/api/uploads
curl -N localhost:8080/api/jobs/<job-id>/events
curl localhost:8080/api/profiles/<profile-id>/components.xml
```

## Development

```bash
go vet ./... && go test ./...
python3 testdata/gen_samples.py   # regenerates testdata/acme.pptx (needs python-pptx, Pillow); nimbus.html is a static fixture
```

## Layout

```
cmd/styleengine     CLI: extract | serve
internal/ooxml      PPTX (OOXML) parser → rawdeck
internal/htmlx      HTML decks measured in headless Chrome (chromedp) → rawdeck
internal/rawdeck    source-neutral intermediate representation
internal/normalize  rawdeck → StyleProfile (palette, type scale, layout clustering, placements)
internal/profile    components.xml schema, parse/validate
internal/store      SQLite (WAL): uploads, profiles + versions, decks, jobs, cache
internal/jobs       SQLite-backed job queue, worker pool, SSE event fan-out
internal/blob       content-addressed local file store
internal/api        HTTP/JSON + SSE
```

## Generation

`POST /api/decks {"profileId","prompt","slides"}` plans and writes content with Gemini (`GEMINI_API_KEY`; models via `GEMINI_PLAN_MODEL` / `GEMINI_FILL_MODEL`, default `gemini-3.8-flash`) or a deterministic offline writer when no key is set. Slides stream over `/api/jobs/{id}/events` as SVG; export with `GET /api/decks/{id}/export.pptx`; rewrite one slide with `POST /api/decks/{id}/slides/{n}/regenerate {"hint"}` (one Fill call).

CLI: `styleengine generate -profile components.xml -prompt "Q3 review" -slides 8 -o deck.pptx [-svg out/] [-data ./data]`. The embedded UI is served at `/`.

## Docker

```bash
docker compose up --build          # http://localhost:8080, data in the styleengine-data volume
GEMINI_API_KEY=... docker compose up --build   # use Gemini instead of the offline writer
```

The image is a static Go binary plus Debian's Chromium (only needed for HTML deck extraction). It runs as a non-root user with `CHROME_NO_SANDBOX=1`, since containers usually lack the user namespaces Chrome's sandbox needs.

## Accounts and API tokens

Every `/api/*` route except signup/login needs a signed-in user; uploads, profiles, decks and jobs are only visible to their owner (others get 404).

```bash
curl -c jar -H 'Content-Type: application/json' -d '{"email":"me@example.com","password":"********"}' localhost:8080/api/auth/signup
curl -b jar -X POST -d '{"name":"scripts"}' localhost:8080/api/tokens      # {"token":"se_..."} shown once
curl -H "Authorization: Bearer se_..." localhost:8080/api/profiles
```

Passwords are bcrypt-hashed; sessions (30-day `HttpOnly`, `SameSite=Lax` cookie) and tokens are stored as SHA-256 hashes. Disable new signups with `-signup=false` or `SIGNUP=off`. Rows created before accounts existed have no owner and are hidden.

## Postgres

SQLite (a file in `-data`) is the default. To share one database between several server instances, point at Postgres instead:

```bash
styleengine serve -db 'postgres://user:pass@host:5432/styleengine?sslmode=disable'   # or DATABASE_URL=...
```

The schema is created on start. Job claiming uses `FOR UPDATE SKIP LOCKED`, so multiple instances can run workers against the same queue. Blobs still live under `-data`; use a shared volume when running more than one instance.

Store tests run against Postgres when `TEST_POSTGRES_DSN` is set (CI does this via a `postgres:16` service).

## Images

Image slots are filled from the slide's `imagePrompt` by a Gemini image model when one is configured:

```bash
GEMINI_API_KEY=... GEMINI_IMAGE_MODEL=gemini-2.5-flash-image styleengine serve
```

The requested aspect ratio is the supported ratio closest to the slot's shape. Each image is stored as a content-addressed blob and the prompt→image mapping is cached (30 days), so the same description is never generated twice. Without `GEMINI_IMAGE_MODEL` (or with the offline writer) slots keep their styled placeholder; a failed image call also falls back to the placeholder instead of failing the deck. The `generate` CLI uses images only when `-data` is given (images are written to its blob store).
