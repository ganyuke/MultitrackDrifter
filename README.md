# Multitrack Drifter

A self-hosted, multi-track video review tool for editors and creators. Ingest source media from mounted storage or USB drives, transcode to HLS via FFmpeg, and review synchronized multi-perspective playback in the browser with markers, regions, and export.

## Features

- **Multi-track playback** — synchronize multiple camera/audio angles with per-track volume control
- **Local storage architecture** — source media stays on mounted storage or local disk; HLS previews are generated and served without exposing originals
- **FFmpeg ingest** — probes source files, creates immutable 480p HLS outputs, records ingest state
- **Web-based UI** — Svelte 5 + Vite frontend with `hls.js`, marker/region annotations, grid presets, and audio mixer
- **Real-time collaboration** — WebSocket hub broadcasts markers and regions to other browser sessions on the same project
- **Export** — CSV, Markdown, JSON, and CMX 3600 EDL export formats
- **Authentication** — session-cookie auth with LDAP support and a dev auth mode for local testing
- **Authorization** — project-owner model for edits, clips, ingest, and timeline placement
- **SQLite persistence** — WAL mode, foreign keys, migrations, integer-ms timing
- **S3 adapter seam** — compile-safe interface for future Garage/S3 object storage

## Requirements

- Go 1.22+
- Node.js 20+
- FFmpeg and FFprobe on `PATH`
- A modern browser

## Setup

```bash
cp .env.example .env
make deps
make build
```

Configure storage roots in `.env`. For quick testing with demo media:

```bash
mkdir -p storage/source storage/hls
make demo-media
./bin/drifter serve
```

Open `http://127.0.0.1:8080`.

## Usage

1. Log in (dev auth accepts any username/password).
2. Create a project.
3. Browse sources and add files as clips.
4. Trigger ingest — FFmpeg creates HLS previews.
5. Review synchronized playback, add markers/regions.
6. Export annotations (CSV, Markdown, JSON, or EDL).

## Repository layout

```
cmd/drifter         CLI entrypoint
internal/           Go packages (auth, config, db, export, ffmpeg, httpapi, ingest, projects, realtime, storage, timeline, webdist)
web/                Svelte + Vite frontend
```

## TODO

- **Advanced timeline** — finer-grained clip placement, trimming, and transitions
- **More export formats** — AAF, FCPX XML
- **Notifications** — alert users when ingest completes or fails
- **Performance optimizations** — segment caching, CDN support, larger ingest worker pools
- **Tests and hardening** — comprehensive integration and end-to-end test coverage

## License

AGPL-3.0-only. See [LICENSE](LICENSE).
