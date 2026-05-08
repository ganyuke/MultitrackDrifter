package storage

import (
	"context"
	"io"
	"time"
)

type ObjectRef struct {
	Adapter string `json:"adapter"`
	Bucket  string `json:"bucket,omitempty"`
	Key     string `json:"key,omitempty"`
	Path    string `json:"path,omitempty"`
}

type ObjectInfo struct {
	Name         string    `json:"name"`
	Ref          ObjectRef `json:"ref"`
	SizeBytes    int64     `json:"sizeBytes"`
	ModifiedUnix int64     `json:"modifiedUnix,omitempty"`
	ETag         string    `json:"etag,omitempty"`
	Device       uint64    `json:"device,omitempty"`
	Inode        uint64    `json:"inode,omitempty"`
	IsPrefix     bool      `json:"isPrefix,omitempty"`
}

type SourceStore interface {
	List(ctx context.Context, prefix string, delimiter string) ([]ObjectInfo, error)
	Open(ctx context.Context, ref ObjectRef) (io.ReadCloser, error)
	Stat(ctx context.Context, ref ObjectRef) (ObjectInfo, error)
}

type HLSStore interface {
	Put(ctx context.Context, ref ObjectRef, r io.Reader, contentType string) error
	List(ctx context.Context, prefix string, delimiter string) ([]ObjectInfo, error)
	Open(ctx context.Context, ref ObjectRef) (io.ReadCloser, error)
	PresignRead(ctx context.Context, ref ObjectRef, ttl time.Duration) (string, error)
	PublicOrSignedURL(ctx context.Context, ref ObjectRef, ttl time.Duration) (string, error)
}
