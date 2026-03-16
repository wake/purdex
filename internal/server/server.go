// internal/server/server.go
package server

import (
	"fmt"
	"net/http"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

type Server struct {
	cfg   config.Config
	store *store.Store
	tmux  tmux.Executor
	mux   *http.ServeMux
}

func New(cfg config.Config, st *store.Store, tx tmux.Executor) *Server {
	s := &Server{cfg: cfg, store: st, tmux: tx, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	sh := NewSessionHandler(s.store, s.tmux)
	s.mux.HandleFunc("GET /api/sessions", sh.List)
	s.mux.HandleFunc("POST /api/sessions", sh.Create)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", sh.Delete)
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = TokenAuth(s.cfg.Token)(h)
	h = IPWhitelist(s.cfg.Allow)(h)
	h = CORS(h)
	return h
}

func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Bind, s.cfg.Port)
	return http.ListenAndServe(addr, s.Handler())
}
