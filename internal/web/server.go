// Package web serves a browser UI and REST API for myplatform.
// It reuses the existing provider and deploy packages directly.
package web

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

type Config struct {
	Addr  string
	Store *Store
}

type Server struct {
	cfg                 Config
	ctx                 context.Context
	store               *Store
	rl                  *rateLimiter
	credentialValidator func(context.Context, string, any) error
}

func NewServer(ctx context.Context, cfg Config) *Server {
	return &Server{
		cfg:   cfg,
		ctx:   ctx,
		store: cfg.Store,
		rl:    newRateLimiter(),
	}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()

	// Public routes (no auth)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// Protected routes (auth required)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/clusters", s.handleClusters)
	mux.HandleFunc("POST /api/deploy", s.handleDeploy)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleJob)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/credentials", s.handleGetCredentials)
	mux.HandleFunc("PUT /api/credentials/{provider}", s.handleSaveCredentials)
	mux.HandleFunc("DELETE /api/credentials/{provider}", s.handleDeleteCredentials)

	// Static files (public — SPA)
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// Wrap everything with security headers + auth middleware
	handler := s.SecurityHeaders(s.AuthMiddleware(mux))

	srv := &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      handler,
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
