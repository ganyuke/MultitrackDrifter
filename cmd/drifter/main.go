package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/multitrack-drifter/internal/auth"
	"github.com/example/multitrack-drifter/internal/config"
	"github.com/example/multitrack-drifter/internal/db"
	"github.com/example/multitrack-drifter/internal/httpapi"
	"github.com/example/multitrack-drifter/internal/ingest"
	"github.com/example/multitrack-drifter/internal/realtime"
	"github.com/example/multitrack-drifter/internal/storage"
	"github.com/example/multitrack-drifter/internal/storage/localstore"
	"github.com/example/multitrack-drifter/internal/storage/s3store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	switch os.Args[1] {
	case "serve":
		if err := serve(ctx, cfg); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case "gc":
		fmt.Println("gc command is intentionally CLI-only; local HLS garbage collection policy is not enabled in this POC")
	default:
		usage()
		os.Exit(2)
	}
}

func serve(ctx context.Context, cfg config.Config) error {
	database, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	source, hls, err := stores(cfg)
	if err != nil {
		return err
	}
	authSvc := auth.New(database, cfg)
	hub := realtime.NewHub()
	worker := ingest.NewWorker(database, cfg, source, hls, hub)
	worker.Start(ctx)
	srv := &http.Server{Addr: cfg.Addr, Handler: httpapi.New(database, cfg, authSvc, source, hls, worker, hub, httpapi.StaticHandler()).Routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("drifter listening on http://%s", cfg.Addr)
	return srv.ListenAndServe()
}

func stores(cfg config.Config) (storage.SourceStore, storage.HLSStore, error) {
	var source storage.SourceStore
	var hls storage.HLSStore
	switch cfg.SourceAdapter {
	case "local":
		s, err := localstore.NewSource(cfg.SourceLocalRoot)
		if err != nil {
			return nil, nil, err
		}
		source = s
	case "s3":
		s, err := s3store.NewSourceFromEnv()
		if err != nil {
			return nil, nil, err
		}
		source = s
	default:
		return nil, nil, fmt.Errorf("unknown SOURCE_ADAPTER %q", cfg.SourceAdapter)
	}
	switch cfg.HLSAdapter {
	case "local":
		h, err := localstore.NewHLS(cfg.HLSLocalRoot, cfg.HLSLocalURLPrefix)
		if err != nil {
			return nil, nil, err
		}
		hls = h
	case "s3":
		h, err := s3store.NewHLSFromEnv()
		if err != nil {
			return nil, nil, err
		}
		hls = h
	default:
		return nil, nil, fmt.Errorf("unknown HLS_ADAPTER %q", cfg.HLSAdapter)
	}
	return source, hls, nil
}

func usage() { fmt.Fprintln(os.Stderr, "usage: drifter serve|gc") }
