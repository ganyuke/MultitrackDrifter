package s3store

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/example/multitrack-drifter/internal/storage"
)

type Source struct{}
type HLS struct{}

func NewSourceFromEnv() (*Source, error) {
	return &Source{}, errors.New("s3 source adapter is a POC seam and is not wired yet")
}

func NewHLSFromEnv() (*HLS, error) {
	return &HLS{}, errors.New("s3 hls adapter is a POC seam and is not wired yet")
}

func (s *Source) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	return nil, errors.New("s3 source list is not implemented")
}
func (s *Source) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	return nil, errors.New("s3 source open is not implemented")
}
func (s *Source) Stat(ctx context.Context, ref storage.ObjectRef) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, errors.New("s3 source stat is not implemented")
}

func (h *HLS) Put(ctx context.Context, ref storage.ObjectRef, r io.Reader, contentType string) error {
	return errors.New("s3 hls put is not implemented")
}
func (h *HLS) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	return nil, errors.New("s3 hls list is not implemented")
}
func (h *HLS) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	return nil, errors.New("s3 hls open is not implemented")
}
func (h *HLS) PresignRead(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	return "", errors.New("s3 hls presign is not implemented")
}
func (h *HLS) PublicOrSignedURL(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	return "", errors.New("s3 hls public url is not implemented")
}
