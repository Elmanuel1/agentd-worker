// Package handoff holds each launched sandbox's work item until its fetch container collects it
// through GET /work-item with its nonce, from its own address, while it runs.
package handoff

import (
	"crypto/subtle"
	"errors"
	"sync"

	"github.com/Elmanuel1/agentd-worker/internal/ids"
)

var (
	// ErrUnknownNonce means the nonce matches no item waiting for collection.
	ErrUnknownNonce = errors.New("nonce is not valid")
	// ErrNotRunning means the nonce's sandbox is not running.
	ErrNotRunning = errors.New("the sandbox is not running")
	// ErrWrongSource means the request came from an address other than the sandbox's.
	ErrWrongSource = errors.New("request did not come from the sandbox")
)

type entry struct {
	work      ids.WorkID
	nonce     ids.Nonce
	secret    ids.Secret
	collected chan struct{}
	// delivering is non-nil while a response is being written; Drop and other requests wait on it.
	delivering chan struct{}
}

// Store is safe for concurrent use. It lives in memory only, so a restarted controller holds no nonces.
type Store struct {
	mu   sync.Mutex
	open map[ids.SessionID]*entry
}

func NewStore() *Store {
	return &Store{open: map[ids.SessionID]*entry{}}
}

// Open holds secret for session under nonce, replacing an uncollected earlier one, and returns a
// channel that closes once the sandbox has collected it.
func (s *Store) Open(session ids.SessionID, work ids.WorkID, nonce ids.Nonce, secret ids.Secret) <-chan struct{} {
	s.lockSettled(session)
	defer s.mu.Unlock()
	e := &entry{work: work, nonce: nonce, secret: secret, collected: make(chan struct{})}
	s.open[session] = e
	return e.collected
}

// Drop forgets session's uncollected item and reports whether there was one. false after Open
// means the sandbox collected it, even in the instant the caller gave up waiting.
func (s *Store) Drop(session ids.SessionID) bool {
	s.lockSettled(session)
	defer s.mu.Unlock()
	if _, ok := s.open[session]; !ok {
		return false
	}
	delete(s.open, session)
	return true
}

// claim finds the entry for nonce and marks it in delivery, so a Drop waits for the outcome.
func (s *Store) claim(nonce ids.Nonce) (ids.SessionID, *entry, error) {
	for {
		s.mu.Lock()
		session, e := s.match(nonce)
		if e == nil {
			s.mu.Unlock()
			return "", nil, ErrUnknownNonce
		}
		if wait := e.delivering; wait != nil {
			s.mu.Unlock()
			<-wait
			continue
		}
		e.delivering = make(chan struct{})
		s.mu.Unlock()
		return session, e, nil
	}
}

// settle ends a delivery; a delivered item closes and its nonce is spent.
func (s *Store) settle(session ids.SessionID, e *entry, delivered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(e.delivering)
	e.delivering = nil
	if delivered {
		delete(s.open, session)
		close(e.collected)
	}
}

// match compares every nonce in constant time, so a hit cannot be told apart by timing.
// The caller holds the lock.
func (s *Store) match(nonce ids.Nonce) (ids.SessionID, *entry) {
	var found ids.SessionID
	var hit *entry
	for session, e := range s.open {
		if nonce != "" && subtle.ConstantTimeCompare([]byte(e.nonce), []byte(nonce)) == 1 {
			found, hit = session, e
		}
	}
	return found, hit
}

// lockSettled takes the lock once no delivery to session is in flight.
func (s *Store) lockSettled(session ids.SessionID) {
	for {
		s.mu.Lock()
		e, ok := s.open[session]
		if !ok || e.delivering == nil {
			return
		}
		wait := e.delivering
		s.mu.Unlock()
		<-wait
	}
}
