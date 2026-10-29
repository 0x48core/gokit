package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type Server struct {
	srv             *http.Server
	shutdownTimeout time.Duration
}

// New creates a server for handler. conf may be nil to use the defaults. The
// server's own errors, such as TLS handshake failures, go to slog.Default.
func New(conf *Config, handler http.Handler) *Server {
	c := Config{}
	if conf != nil {
		c = *conf
	}
	c = c.withDefaults()

	return &Server{
		srv: &http.Server{
			Addr:              c.Addr,
			Handler:           handler,
			ReadTimeout:       c.ReadTimeout,
			ReadHeaderTimeout: c.ReadHeaderTimeout,
			WriteTimeout:      c.WriteTimeout,
			IdleTimeout:       c.IdleTimeout,
			ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
		},
		shutdownTimeout: c.ShutdownTimeout,
	}
}

// Run listens on the configured address and serves until ctx is done. See Serve.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen %s: %w", s.srv.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx is done, then stops accepting and
// waits up to ShutdownTimeout for in-flight requests to finish. It returns nil
// after a clean shutdown, and an error if serving fails or in-flight requests
// outlast the timeout, in which case their connections are closed.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("httpserver: serve: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	err := s.srv.Shutdown(shutdownCtx)
	if serveErr := <-errCh; !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("httpserver: serve: %w", serveErr)
	}
	if err != nil {
		_ = s.srv.Close()
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}
	return nil
}
