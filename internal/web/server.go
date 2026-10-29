// Package web serves a browser UI and REST API for myplatform.
// It reuses the existing provider and deploy packages directly.
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/example/myplatform/internal/provider"
)

//go:embed static/*
var staticFiles embed.FS

type Config struct {
	Addr            string
	ProviderOptions provider.Options
}

type Server struct {
	cfg  Config
	ctx  context.Context
	mu   sync.RWMutex
	opts provider.Options
}

func (s *Server) getOpts() provider.Options {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts
}

func (s *Server) setOpts(o provider.Options) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opts = o
}

func NewServer(ctx context.Context, cfg Config) *Server {
	return &Server{cfg: cfg, ctx: ctx, opts: cfg.ProviderOptions}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/clusters", s.handleClusters)
	mux.HandleFunc("POST /api/deploy", s.handleDeploy)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("POST /api/config", s.handleUpdateConfig)

	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	srv := &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // background jobs may take minutes
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		<-s.ctx.Done()
		srv.Shutdown(context.Background())
	}()

	fmt.Printf("myplatform web UI ready at http://%s\n", s.cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
