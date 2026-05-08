# Multitrack Drifter POC

This repository is a buildable proof-of-concept for the uploaded Multitrack Drifter RFC. It is intentionally local-first: the default configuration uses a read-only local source tree and a separate local HLS output tree, so you can test with mounted disks, USB drives, or a scratch directory before wiring S3/Garage and production LDAP.

## What is implemented

- Go 1.22+ single binary serving REST APIs, WebSockets, authenticated local HLS, and embedded static assets.
- Svelte + Vite frontend source using `hls.js`, browser-local playback preferences, marker/region UI, grid presets, and an audio mixer scaffold. The entrypoint uses the Svelte 5 `mount()` API so `latest` Svelte works with the static Vite build.
- SQLite through `modernc.org/sqlite` with WAL, busy timeout, foreign keys, migrations, and integer-ms timing.
- Session-cookie auth with LDAP support plus a development auth mode for local testing.
- Project owner authorization for project edits, clips, ingest, and timeline placement.
- Local `SourceStore` without mutation methods and local writable `HLSStore`.
- FFmpeg ingest worker that probes sources, creates immutable 480p HLS outputs, records ingest state, and does not expose source files to browsers.
- Project-scoped WebSocket hub with read pump + single write pump per connection, bounded outbound buffers, ping/pong deadlines, and same-origin WebSocket upgrade checks.
- CSV, Markdown, JSON, and basic CMX 3600 EDL exports.

The S3 adapter package is present as a compile-safe seam with explicit `not implemented` errors. The local device path is ready for end-to-end testing; Garage/S3 credential wiring remains the primary next implementation step.

## Requirements

- Go 1.22 or newer.
- Node.js 20 or newer for building the Svelte frontend.
- FFmpeg and FFprobe on `PATH`.
- A modern browser.

## Local storage-device setup

Use two separate directories. They may live on different physical devices:

- **Source root**: original media input. The app reads and probes it for ingest only. It is never served to browsers.
- **HLS root**: generated review previews. The app writes HLS playlists/segments here and serves them through authenticated `/media/hls/...` routes.

Example with two mounted devices:

```bash
sudo mkdir -p /mnt/drifter-source /mnt/drifter-hls
sudo chown -R "$USER":"$USER" /mnt/drifter-source /mnt/drifter-hls
cp .env.example .env
```

Edit `.env`:

```env
SOURCE_ADAPTER=local
SOURCE_LOCAL_ROOT=/mnt/drifter-source

HLS_ADAPTER=local
HLS_LOCAL_ROOT=/mnt/drifter-hls
HLS_LOCAL_URL_PREFIX=/media/hls
```

For quick local testing without mounted devices, keep the default relative paths and create demo media:

```bash
mkdir -p storage/source storage/hls
make demo-media
```

## Build and run

```bash
cp .env.example .env
make deps
make build
./bin/drifter serve
```

Open `http://127.0.0.1:8080`.

For a faster backend-only loop, use:

```bash
make run
```

`make run` serves the embedded fallback frontend unless you have run `make web` first.

## Local POC walkthrough

1. Log in with development auth. Any username/password works while `DEV_AUTH_ENABLED=true`.
2. Create a project.
3. Open the project and browse sources. The source browser supports prefix/delimiter-style folder traversal.
4. Add one or more files as clips. Defaults infer perspective names from the parent folder and use integer millisecond wallclock placement.
5. Trigger ingest. FFmpeg writes immutable HLS outputs beneath the HLS root.
6. Use the playback manifest to review generated playlists. Local HLS URLs are served only through authenticated Go routes.
7. Add markers and regions. They persist in SQLite and broadcast live to other browser sessions on the same project.
8. Download exports from the project page.

## API smoke test

```bash
curl -i -c cookies.txt -H 'content-type: application/json'   -d '{"username":"alice","password":"dev"}'   http://127.0.0.1:8080/api/login

curl -b cookies.txt -H 'content-type: application/json'   -d '{"name":"Local test","description":"USB source and SSD HLS"}'   http://127.0.0.1:8080/api/projects

curl -b cookies.txt 'http://127.0.0.1:8080/api/projects/1/sources?prefix=&delimiter=/'

# Optional multi-stream clip selection after inspecting ffprobe stream indexes:
curl -b cookies.txt -H 'content-type: application/json' \
  -d '{"sourcePath":"Alice/gameplay.mp4","perspective":"Alice","track":"Alice / Mic","kind":"audio","streamIndex":1,"wallclockStartMs":0}' \
  http://127.0.0.1:8080/api/projects/1/assets
```

## Production notes

- Put Caddy in front of the Go app for HTTPS and set `SECURE_COOKIES=true`.
- Set `DEV_AUTH_ENABLED=false` and configure the LDAP variables.
- Keep source credentials or mount options read-only. The Go `SourceStore` interface intentionally has no mutating methods.
- Back up the SQLite database and HLS root independently. Generated HLS paths are immutable; do not point HLS output at the source tree.

Example Caddyfile:

```caddyfile
drifter.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

## Repository layout

```text
/cmd/drifter                 CLI entrypoint
/internal/auth               LDAP/dev auth and sessions
/internal/config             environment config
/internal/db                 SQLite open and migrations
/internal/export             CSV/Markdown/JSON/EDL exports
/internal/ffmpeg             ffprobe/ffmpeg command planning
/internal/httpapi            REST, WebSocket, local HLS, static serving
/internal/ingest             asynchronous ingest worker
/internal/projects           shared project models
/internal/realtime           gorilla/websocket hub with single writer
/internal/storage            adapter interfaces
/internal/storage/localstore local source and HLS stores
/internal/storage/s3store    S3 seam for future Garage wiring
/internal/timeline           integer-ms conversion helpers
/internal/webdist            embedded frontend dist
/web                         Svelte + Vite app
```
