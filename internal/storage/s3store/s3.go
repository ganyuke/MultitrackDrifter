package s3store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/ganyuke/multitrack-drifter/internal/hlsassets"
	"github.com/ganyuke/multitrack-drifter/internal/storage"
)

type Source struct {
	client   *s3.Client
	bucket   string
	endpoint string
	root     string
}

type HLS struct {
	client   *s3.Client
	bucket   string
	endpoint string
	root     string
	cache    *urlCache
}

type urlCache struct {
	mu  int
	m   map[string]signedURL
	ttl time.Duration
}

type signedURL struct {
	url       string
	expiresAt time.Time
}

func NewSource(ctx context.Context, cfg S3Config) (*Source, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, cfg.SessionToken,
		)))
	}
	if cfg.Endpoint != "" {
		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               cfg.Endpoint,
				SigningRegion:     cfg.Region,
				HostnameImmutable: true,
			}, nil
		})
		opts = append(opts, awsconfig.WithEndpointResolverWithOptions(customResolver))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.HTTPClient = httpClient
		o.UsePathStyle = true
	})
	return &Source{
		client:   client,
		bucket:   cfg.Bucket,
		endpoint: cfg.Endpoint,
		root:     cfg.Root,
	}, nil
}

func NewHLS(ctx context.Context, cfg S3Config) (*HLS, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("S3 bucket is required")
	}
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.AccessKey != "" && cfg.SecretKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, cfg.SessionToken,
		)))
	}
	if cfg.Endpoint != "" {
		customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL:               cfg.Endpoint,
				SigningRegion:     cfg.Region,
				HostnameImmutable: true,
			}, nil
		})
		opts = append(opts, awsconfig.WithEndpointResolverWithOptions(customResolver))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	httpClient := &http.Client{Timeout: 60 * time.Second}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.HTTPClient = httpClient
		o.UsePathStyle = true
	})
	return &HLS{
		client:   client,
		bucket:   cfg.Bucket,
		endpoint: cfg.Endpoint,
		root:     cfg.Root,
		cache:    &urlCache{ttl: 5 * time.Minute},
	}, nil
}

func (s *Source) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String(s.key(prefix)),
		Delimiter: aws.String(delimiter),
	}
	paginator := s3.NewListObjectsV2Paginator(s.client, input)
	var out []storage.ObjectInfo
	for paginator.HasMorePages() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			out = append(out, objectInfoFromS3(obj, s.adapter(), s.bucket))
		}
		for _, prefixObj := range page.CommonPrefixes {
			out = append(out, storage.ObjectInfo{
				Name:     path.Base(aws.ToString(prefixObj.Prefix)),
				Ref:      storage.ObjectRef{Adapter: s.adapter(), Bucket: s.bucket, Key: aws.ToString(prefixObj.Prefix)},
				IsPrefix: true,
			})
		}
	}
	return out, nil
}

func (s *Source) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	key := ref.Key
	if key == "" {
		key = s.key(ref.Path)
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return result.Body, nil
}

func (s *Source) Stat(ctx context.Context, ref storage.ObjectRef) (storage.ObjectInfo, error) {
	key := ref.Key
	if key == "" {
		key = s.key(ref.Path)
	}
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	return storage.ObjectInfo{
		Name:         path.Base(key),
		Ref:          storage.ObjectRef{Adapter: s.adapter(), Bucket: s.bucket, Key: key},
		SizeBytes:    aws.ToInt64(result.ContentLength),
		ModifiedUnix: result.LastModified.Unix(),
		ETag:         strings.Trim(strings.TrimSpace(aws.ToString(result.ETag)), `"`),
	}, nil
}

func (s *Source) PresignRead(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	key := ref.Key
	if key == "" {
		key = s.key(ref.Path)
	}
	presignClient := s3.NewPresignClient(s.client)
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}
	return result.URL, nil
}

func (h *HLS) Put(ctx context.Context, ref storage.ObjectRef, r io.Reader, contentType string) error {
	key := h.key(ref.Path)
	if key == "" {
		return errors.New("HLS put requires a path")
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	cacheControl := "public, max-age=31536000, immutable"
	var expires *time.Time
	if strings.EqualFold(contentType, hlsassets.PlaylistContentType) || strings.HasSuffix(strings.ToLower(key), ".m3u8") {
		cacheControl = "no-store"
	} else {
		expiresAt := time.Now().Add(365 * 24 * time.Hour)
		expires = &expiresAt
	}

	_, err := h.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(h.bucket),
		Key:          aws.String(key),
		Body:         r,
		ContentType:  aws.String(contentType),
		CacheControl: aws.String(cacheControl),
		Expires:      expires,
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

func (h *HLS) List(ctx context.Context, prefix string, delimiter string) ([]storage.ObjectInfo, error) {
	input := &s3.ListObjectsV2Input{
		Bucket:    aws.String(h.bucket),
		Prefix:    aws.String(h.key(prefix)),
		Delimiter: aws.String(delimiter),
	}
	paginator := s3.NewListObjectsV2Paginator(h.client, input)
	var out []storage.ObjectInfo
	for paginator.HasMorePages() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			out = append(out, objectInfoFromS3(obj, h.adapter(), h.bucket))
		}
		for _, prefixObj := range page.CommonPrefixes {
			out = append(out, storage.ObjectInfo{
				Name:     path.Base(strings.TrimSuffix(aws.ToString(prefixObj.Prefix), "/")),
				Ref:      storage.ObjectRef{Adapter: h.adapter(), Bucket: h.bucket, Key: aws.ToString(prefixObj.Prefix)},
				IsPrefix: true,
			})
		}
	}
	return out, nil
}

func (h *HLS) Open(ctx context.Context, ref storage.ObjectRef) (io.ReadCloser, error) {
	key := ref.Key
	if key == "" {
		key = h.key(ref.Path)
	}
	result, err := h.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return result.Body, nil
}

func (h *HLS) Stat(ctx context.Context, ref storage.ObjectRef) (storage.ObjectInfo, error) {
	key := ref.Key
	if key == "" {
		key = h.key(ref.Path)
	}
	result, err := h.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return storage.ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	return storage.ObjectInfo{
		Name:         path.Base(key),
		Ref:          storage.ObjectRef{Adapter: h.adapter(), Bucket: h.bucket, Key: key},
		SizeBytes:    aws.ToInt64(result.ContentLength),
		ModifiedUnix: result.LastModified.Unix(),
		ETag:         strings.Trim(strings.TrimSpace(aws.ToString(result.ETag)), `"`),
	}, nil
}

func (h *HLS) PresignRead(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	key := ref.Key
	if key == "" {
		key = h.key(ref.Path)
	}
	presignClient := s3.NewPresignClient(h.client)
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}
	return result.URL, nil
}

func (h *HLS) PublicOrSignedURL(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	if h.endpoint == "" {
		return "", errors.New("publicOrSignedURL requires S3 endpoint to be configured")
	}
	key := ref.Key
	if key == "" {
		key = h.key(ref.Path)
	}
	u, err := url.Parse(h.endpoint)
	if err != nil {
		return "", err
	}
	base := &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
	}
	if h.endpoint != "" {
		base.Path = path.Join(base.Path, h.bucket, key)
	} else {
		base.Host = fmt.Sprintf("%s.s3.amazonaws.com", h.bucket)
		base.Path = "/" + key
	}
	return base.String(), nil
}

func objectInfoFromS3(obj s3Types.Object, adapter, bucket string) storage.ObjectInfo {
	key := aws.ToString(obj.Key)
	return storage.ObjectInfo{
		Name:         path.Base(key),
		Ref:          storage.ObjectRef{Adapter: adapter, Bucket: bucket, Key: key},
		SizeBytes:    aws.ToInt64(obj.Size),
		ModifiedUnix: obj.LastModified.Unix(),
		ETag:         strings.Trim(strings.TrimSpace(aws.ToString(obj.ETag)), `"`),
	}
}

func (s *Source) adapter() string { return "s3" }
func (h *HLS) adapter() string    { return "s3" }

func (h *HLS) key(rel string) string {
	rel = strings.TrimSpace(strings.TrimLeft(strings.ReplaceAll(rel, "\\", "/"), "/"))
	if rel == "" || rel == "." {
		return h.root
	}
	if h.root == "" {
		return rel
	}
	return strings.TrimSuffix(h.root, "/") + "/" + rel
}

func (s *Source) key(rel string) string {
	rel = strings.TrimSpace(strings.TrimLeft(strings.ReplaceAll(rel, "\\", "/"), "/"))
	if rel == "" || rel == "." {
		return s.root
	}
	if s.root == "" {
		return rel
	}
	return strings.TrimSuffix(s.root, "/") + "/" + rel
}

type S3Config struct {
	Endpoint     string
	Region       string
	Bucket       string
	AccessKey    string
	SecretKey    string
	SessionToken string
	Root         string
}

var _ storage.SourceStore = (*Source)(nil)
var _ storage.HLSStore = (*HLS)(nil)
