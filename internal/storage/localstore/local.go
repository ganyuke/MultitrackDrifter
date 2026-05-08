package localstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/example/multitrack-drifter/internal/storage"
)

type Source struct {
	root string
}

type HLS struct {
	root      string
	urlPrefix string
}

func NewSource(root string) (*Source, error) {
	abs, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}
	return &Source{root: abs}, nil
}

func NewHLS(root string, urlPrefix string) (*HLS, error) {
	abs, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(urlPrefix) == "" {
		urlPrefix = "/media/hls"
	}
	return &HLS{root: abs, urlPrefix: "/" + strings.Trim(strings.TrimSpace(urlPrefix), "/")}, nil
}

func normalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("local root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func (s *Source) ResolvePath(rel string) (string, error) { return resolveUnderRoot(s.root, rel) }
func (h *HLS) ResolvePath(rel string) (string, error)    { return resolveUnderRoot(h.root, rel) }

func (s *Source) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	return listLocal(ctx, s.root, "local", prefix, delimiter)
}

func (s *Source) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	return openLocal(ctx, s.root, ref.Path)
}

func (s *Source) Stat(ctx context.Context, ref storage.ObjectRef) (storage.ObjectInfo, error) {
	return statLocal(ctx, s.root, "local", ref.Path)
}

func (h *HLS) Put(ctx context.Context, ref storage.ObjectRef, r io.Reader, contentType string) error {
	_ = contentType
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p, err := resolveUnderRoot(h.root, ref.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, p)
}

func (h *HLS) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	return listLocal(ctx, h.root, "local", prefix, delimiter)
}

func (h *HLS) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	return openLocal(ctx, h.root, ref.Path)
}

func (h *HLS) PresignRead(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	return h.PublicOrSignedURL(ctx, ref, ttl)
}

func (h *HLS) PublicOrSignedURL(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	_ = ctx
	_ = ttl
	clean, err := cleanRel(ref.Path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return path.Join(h.urlPrefix, strings.Join(parts, "/")), nil
}

func listLocal(ctx context.Context, root string, adapter string, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	_ = delimiter
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	rel, err := cleanRel(prefix)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		rel = ""
	}
	dir, err := resolveUnderRoot(root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]storage.ObjectInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		childRel := filepath.ToSlash(filepath.Join(rel, entry.Name()))
		item := objectInfo(adapter, childRel, info)
		if entry.IsDir() {
			item.IsPrefix = true
			item.Ref.Path = strings.TrimRight(childRel, "/") + "/"
			item.Name = entry.Name() + "/"
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsPrefix != out[j].IsPrefix {
			return out[i].IsPrefix
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func openLocal(ctx context.Context, root string, rel string) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	p, err := resolveUnderRoot(root, rel)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func statLocal(ctx context.Context, root string, adapter string, rel string) (storage.ObjectInfo, error) {
	select {
	case <-ctx.Done():
		return storage.ObjectInfo{}, ctx.Err()
	default:
	}
	p, err := resolveUnderRoot(root, rel)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return storage.ObjectInfo{}, err
	}
	return objectInfo(adapter, rel, info), nil
}

func objectInfo(adapter string, rel string, info os.FileInfo) storage.ObjectInfo {
	clean := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(rel), string(filepath.Separator)))
	if clean == "." {
		clean = ""
	}
	obj := storage.ObjectInfo{
		Name:         filepath.Base(clean),
		Ref:          storage.ObjectRef{Adapter: adapter, Path: clean},
		SizeBytes:    info.Size(),
		ModifiedUnix: info.ModTime().Unix(),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if runtime.GOOS != "windows" {
			obj.Device = uint64(stat.Dev)
			obj.Inode = uint64(stat.Ino)
		}
	}
	return obj
}

func resolveUnderRoot(root string, rel string) (string, error) {
	clean, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, clean)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rootWithSep := root + string(filepath.Separator)
	if abs != root && !strings.HasPrefix(abs, rootWithSep) {
		return "", fmt.Errorf("path escapes local root: %q", rel)
	}
	return abs, nil
}

func cleanRel(rel string) (string, error) {
	rel = strings.TrimSpace(strings.TrimLeft(filepath.ToSlash(rel), "/"))
	if rel == "" {
		return ".", nil
	}
	clean := path.Clean(rel)
	if clean == "." {
		return ".", nil
	}
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("invalid relative path: %q", rel)
	}
	return filepath.FromSlash(clean), nil
}
