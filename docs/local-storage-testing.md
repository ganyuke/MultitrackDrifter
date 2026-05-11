# Testing with local storage devices

The POC treats source media and generated HLS as separate adapter roles even when both are local filesystems.

## 1. Mount or create roots

For a two-device test:

```bash
sudo mkdir -p /mnt/drifter-source /mnt/drifter-hls
sudo mount /dev/disk/by-label/DRIFTER_SRC /mnt/drifter-source
sudo mount /dev/disk/by-label/DRIFTER_HLS /mnt/drifter-hls
sudo chown -R "$USER":"$USER" /mnt/drifter-source /mnt/drifter-hls
```

For a same-disk development test:

```bash
mkdir -p storage/source storage/hls
```

## 2. Configure `.env`

Mounted devices:

```env
SOURCE_ADAPTER=local
SOURCE_LOCAL_ROOT=/mnt/drifter-source

HLS_ADAPTER=local
HLS_LOCAL_ROOT=/mnt/drifter-hls
HLS_LOCAL_URL_PREFIX=/media/hls
```

Development directories:

```env
SOURCE_ADAPTER=local
SOURCE_LOCAL_ROOT=./storage/source

HLS_ADAPTER=local
HLS_LOCAL_ROOT=./storage/hls
HLS_LOCAL_URL_PREFIX=/media/hls
```

The source root is ingest-only. Do not configure it under `HLS_LOCAL_URL_PREFIX`, and do not point `HLS_LOCAL_ROOT` at the source root.

## 3. Add local source files

Use folders to model perspectives:

```text
/mnt/drifter-source/
  Alice/
    gameplay.mp4
    mic.wav
  Bob/
    gameplay.mp4
    mic.wav
```

The source browser uses prefix and delimiter traversal, so large roots are browsed one folder level at a time.


### Optional: add a specific media stream

The UI adds the first playable stream by default. For multi-stream test files, use the REST API to select a specific FFprobe stream index after logging in:

```bash
curl -b cookies.txt -H 'content-type: application/json' \
  -d '{"sourcePath":"Alice/gameplay.mp4","perspective":"Alice","track":"Alice / Discord","kind":"audio","streamIndex":2,"wallclockStartMs":0}' \
  http://127.0.0.1:8080/api/projects/1/assets
```

Use `ffprobe -hide_banner -show_streams /mnt/drifter-source/Alice/gameplay.mp4` to identify stream indexes.

## 4. Run the app

```bash
cp .env.example .env
make deps
make build
./bin/drifter serve
```

Open `http://127.0.0.1:8080`, log in, create a project, and add source files as clips.

## 5. Ingest and verify HLS

Click **Trigger ingest**. The worker runs FFprobe/FFmpeg and writes immutable HLS assets to the HLS root, for example:

```text
/mnt/drifter-hls/previews/playlists/<hls_asset_id>.m3u8
/mnt/drifter-hls/previews/hls/<sha[0:2]>/<sha[2:4]>/<sha>.ts
```

The playback manifest returns `/media/hls/...` URLs only. Original source paths are never sent as playback URLs.

## 6. Quick FFmpeg test clip

```bash
make demo-media
```

This creates two short MP4s under `storage/source/Alice` and `storage/source/Bob` for a local-local smoke test.

## 7. Troubleshooting

- `ffprobe: executable file not found`: set `FFPROBE_BIN` or install FFmpeg.
- Ingest job remains `FAILED`: inspect `/api/projects/<id>/ingest-jobs` or the project UI, then retry ingest.
- Browser 401s on HLS: log in again; local HLS routes require the secure session cookie.
- Empty playback manifest: ingest has not succeeded, or the clip has no generated HLS asset yet.
