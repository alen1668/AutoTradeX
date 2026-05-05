package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/web/middleware"
)

type Server struct {
	addr   string
	router *chi.Mux
	server *http.Server
	log    zerolog.Logger
}

func New(addr string, log zerolog.Logger) *Server {
	r := chi.NewRouter()
	r.Use(middleware.TraceID)
	r.Use(middleware.RequestLogger(log))
	r.Use(middleware.Recoverer(log))
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return &Server{
		addr:   addr,
		router: r,
		log:    log,
	}
}

func (s *Server) Router() chi.Router { return s.router }

func (s *Server) Start(ctx context.Context) error {
	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.log.Info().Str("addr", s.addr).Msg("http listening")
	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		return err
	}
}

func (s *Server) shutdown() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.log.Info().Msg("http shutting down")
	return s.server.Shutdown(ctx)
}
