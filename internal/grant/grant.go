// Package grant manages just-in-time read grants for one run.
//
// Grants only ever cover reading: writing a sensitive path is never
// grantable. The store keeps grants in memory for fast FUSE decisions
// and appends every grant to a JSONL file so the run's exposure manifest
// and any later audit can replay exactly what was approved, by whom, and
// until when.
package grant

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Grant is one read-only, time-limited approval for one workspace path.
type Grant struct {
	// Path is the granted workspace-relative path. A grant on a
	// directory covers everything below it.
	Path string `json:"path"`
	// TTL is the requested lifetime as written in the approval flow.
	TTL       string    `json:"ttl"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// Approver records who approved (e.g. "terminal" or an operator
	// name passed via READSCOPE_APPROVER).
	Approver string `json:"approver"`
	// Reason is the free-text justification captured at approval time.
	Reason string `json:"reason"`
}

// Covers reports whether the grant applies to path (exact match or the
// grant was issued for a parent directory).
func (g Grant) Covers(path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+path)), "/")
	if path == g.Path {
		return true
	}
	return strings.HasPrefix(path, g.Path+"/")
}

// Active reports whether the grant is still valid at the given time.
func (g Grant) Active(at time.Time) bool {
	return at.Before(g.ExpiresAt)
}

// Store is the in-memory grant set for one run, persisted as JSONL.
type Store struct {
	mu     sync.Mutex
	grants map[string]Grant
	now    func() time.Time
	w      io.Writer
}

// NewStore returns a store. If sink is non-nil, every Add is appended to
// it as one JSON line (the run's grants.jsonl).
func NewStore(sink io.Writer) *Store {
	return &Store{
		grants: map[string]Grant{},
		now:    time.Now,
		w:      sink,
	}
}

// Add records a grant, replacing any previous grant for the same path.
func (s *Store) Add(g Grant) error {
	g.Path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+g.Path)), "/")
	if g.Path == "" || g.Path == "." {
		return fmt.Errorf("grant path is empty")
	}
	if g.ExpiresAt.IsZero() {
		if g.GrantedAt.IsZero() {
			g.GrantedAt = s.now()
		}
		g.ExpiresAt = g.GrantedAt.Add(30 * time.Minute)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		line, err := json.Marshal(g)
		if err != nil {
			return fmt.Errorf("encode grant: %w", err)
		}
		if _, err := s.w.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("persist grant: %w", err)
		}
	}
	s.grants[g.Path] = g
	return nil
}

// Check returns the active grant covering path at the given time, if any.
// Expired grants are ignored and do not match.
func (s *Store) Check(path string, at time.Time) (Grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkLocked(path, at)
}

func (s *Store) checkLocked(path string, at time.Time) (Grant, bool) {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+path)), "/")
	if path == "" || path == "." {
		return Grant{}, false
	}
	// Exact grant first, then the closest enclosing directory grant.
	if g, ok := s.grants[path]; ok && g.Active(at) {
		return g, true
	}
	best := ""
	var found Grant
	for p, g := range s.grants {
		if !g.Covers(path) || !g.Active(at) {
			continue
		}
		if len(p) > len(best) {
			best, found = p, g
		}
	}
	return found, best != ""
}

// Active returns all unexpired grants sorted by path.
func (s *Store) Active(at time.Time) []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		if g.Active(at) {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// All returns every recorded grant sorted by path, including ones whose
// TTL already expired - the manifest must show what the run was granted,
// not just what is still valid.
func (s *Store) All() []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
