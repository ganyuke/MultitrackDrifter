package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ganyuke/multitrack-drifter/internal/config"
	"github.com/ganyuke/multitrack-drifter/internal/hlsassets"
	"github.com/ganyuke/multitrack-drifter/internal/storage"
)

type memoryHLSStore struct {
	mu      sync.Mutex
	objects map[string]storedObject
}

type storedObject struct {
	body        []byte
	contentType string
}

func newMemoryHLSStore() *memoryHLSStore {
	return &memoryHLSStore{objects: map[string]storedObject{}}
}

func (m *memoryHLSStore) Put(ctx context.Context, ref storage.ObjectRef, r io.Reader, contentType string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.objects[ref.Path] = storedObject{body: body, contentType: contentType}
	m.mu.Unlock()
	return nil
}

func (m *memoryHLSStore) List(context.Context, string, string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

func (m *memoryHLSStore) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	m.mu.Lock()
	obj, ok := m.objects[ref.Path]
	m.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(obj.body)), nil
}

func (m *memoryHLSStore) Stat(ctx context.Context, ref storage.ObjectRef) (storage.ObjectInfo, error) {
	select {
	case <-ctx.Done():
		return storage.ObjectInfo{}, ctx.Err()
	default:
	}
	m.mu.Lock()
	obj, ok := m.objects[ref.Path]
	m.mu.Unlock()
	if !ok {
		return storage.ObjectInfo{}, os.ErrNotExist
	}
	return storage.ObjectInfo{Name: path.Base(ref.Path), Ref: ref, SizeBytes: int64(len(obj.body))}, nil
}

func (m *memoryHLSStore) PresignRead(context.Context, storage.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

func (m *memoryHLSStore) PublicOrSignedURL(context.Context, storage.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

func TestPublishContentAddressedHLSHashesSegmentsAndRewritesPlaylist(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	segmentA := []byte("segment-a-bytes")
	segmentB := []byte("segment-b-bytes")
	writeFile(t, filepath.Join(tmp, "seg_000.ts"), segmentA)
	writeFile(t, filepath.Join(tmp, "seg_001.ts"), segmentB)
	writeFile(t, filepath.Join(tmp, hlsassets.PlaylistFilename), []byte(strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:3",
		"#EXTINF:2.000,",
		"seg_000.ts",
		"#EXTINF:2.000,",
		"seg_001.ts",
		"#EXT-X-ENDLIST",
		"",
	}, "\n")))

	store := newMemoryHLSStore()
	worker := &Worker{cfg: config.Config{HLSAdapter: "local"}, hls: store}
	playlistPath, err := worker.publishContentAddressedHLS(ctx, tmp, 42)
	if err != nil {
		t.Fatalf("publishContentAddressedHLS returned error: %v", err)
	}
	if playlistPath != hlsassets.PlaylistPath(42) {
		t.Fatalf("playlist path = %q, want %s", playlistPath, hlsassets.PlaylistPath(42))
	}

	playlist := string(store.objects[playlistPath].body)
	if strings.Contains(playlist, "seg_000.ts") || strings.Contains(playlist, "seg_001.ts") {
		t.Fatalf("playlist still contains generated segment names:\n%s", playlist)
	}
	if got := store.objects[playlistPath].contentType; got != hlsassets.PlaylistContentType {
		t.Fatalf("playlist content type = %q, want %q", got, hlsassets.PlaylistContentType)
	}

	for _, segment := range [][]byte{segmentA, segmentB} {
		segmentPath := expectedSegmentPath(segment)
		obj, ok := store.objects[segmentPath]
		if !ok {
			t.Fatalf("missing uploaded segment %q", segmentPath)
		}
		if !bytes.Equal(obj.body, segment) {
			t.Fatalf("uploaded segment %q has wrong bytes", segmentPath)
		}
		if obj.contentType != hlsassets.SegmentContentType {
			t.Fatalf("segment content type = %q, want %q", obj.contentType, hlsassets.SegmentContentType)
		}
		if !strings.Contains(playlist, "../"+strings.TrimPrefix(segmentPath, hlsassets.RootDir+"/")) {
			t.Fatalf("playlist does not reference content-addressed segment %q:\n%s", segmentPath, playlist)
		}
	}
}

func TestContentAddressedSegmentPathChangesWhenBytesChange(t *testing.T) {
	first := expectedSegmentPath([]byte("first"))
	second := expectedSegmentPath([]byte("second"))
	if first == second {
		t.Fatalf("different segment bytes produced identical paths: %q", first)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func expectedSegmentPath(data []byte) string {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	return hlsassets.SegmentPath(digest)
}
