package handoff

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

// NonceHeader carries the nonce, under the name Anthropic's dispatcher reads.
const NonceHeader = "X-CMA-Assignment-Nonce"

// Item is the body of GET /work-item: the five fields Anthropic's worker script reads.
type Item struct {
	SessionID     ids.SessionID     `json:"session_id"`
	WorkID        ids.WorkID        `json:"work_id"`
	EnvironmentID ids.EnvironmentID `json:"environment_id"`
	BaseURL       string            `json:"base_url"`
	Secret        string            `json:"secret"`
}

// Server answers GET /work-item from a Store.
type Server struct {
	store   *Store
	env     ids.EnvironmentID
	baseURL string
	log     *slog.Logger
}

// NewServer serves store's items, telling each sandbox env and the agentd base URL.
func NewServer(store *Store, env ids.EnvironmentID, baseURL string, log *slog.Logger) *Server {
	return &Server{store: store, env: env, baseURL: baseURL, log: log}
}

// Handler routes GET /work-item and GET /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /work-item", s.workItem)
	return mux
}

func (s *Server) workItem(w http.ResponseWriter, r *http.Request) {
	// HEAD matches the GET pattern but writes no body, so it must not spend the nonce.
	if r.Method != http.MethodGet {
		http.Error(w, "only GET collects a work item", http.StatusMethodNotAllowed)
		return
	}
	session, e, err := s.authorize(r)
	if err != nil {
		s.log.Info("work item refused", "remote", r.RemoteAddr, "reason", err.Error())
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	item := Item{SessionID: session, WorkID: e.work, EnvironmentID: s.env, BaseURL: s.baseURL, Secret: e.secret.Reveal()}
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(item)
	if err == nil {
		err = http.NewResponseController(w).Flush()
	}
	s.store.settle(session, e, err == nil)
	if err != nil {
		s.log.Warn("work item write failed; keeping it for the sandbox's retry", "session_id", session, "error", err)
		return
	}
	s.log.Info("work item collected", "session_id", session)
}

// authorize claims the nonce's entry, then checks the caller's address against the sandbox's.
// The source is the TCP peer alone; no forwarding header is read.
func (s *Server) authorize(r *http.Request) (ids.SessionID, *entry, error) {
	session, e, err := s.store.claim(ids.Nonce(r.Header.Get(NonceHeader)))
	if err != nil {
		return "", nil, err
	}
	addr, running, err := s.store.locator.Address(r.Context(), session)
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	peer, peerErr := netip.ParseAddr(host)
	switch {
	case err != nil:
		err = errors.Join(ErrNotRunning, err)
	case !running:
		err = ErrNotRunning
	case peerErr != nil || peer.Unmap() != addr.Unmap():
		err = ErrWrongSource
	}
	if err != nil {
		s.store.settle(session, e, false)
		return "", nil, err
	}
	return session, e, nil
}

// Serve serves handler over TLS 1.2 or later until ctx ends, then lets requests in flight finish.
func Serve(ctx context.Context, addr string, cert tls.Certificate, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { errc <- srv.ListenAndServeTLS("", "") })
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err := srv.Shutdown(shutdown)
	if served := <-errc; !errors.Is(served, http.ErrServerClosed) {
		return errors.Join(err, served)
	}
	return err
}
