package grant

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC) }

func newTestStore(t *testing.T, sink interface{ Write(p []byte) (int, error) }) *Store {
	t.Helper()
	s := NewStore(sink)
	s.now = fixedNow
	return s
}

func TestAddPersistsJSONL(t *testing.T) {
	var buf bytes.Buffer
	s := newTestStore(t, &buf)

	g := Grant{
		Path:      ".env",
		TTL:       "30m",
		GrantedAt: fixedNow(),
		ExpiresAt: fixedNow().Add(30 * time.Minute),
		Approver:  "terminal",
		Reason:    "approved in terminal",
	}
	if err := s.Add(g); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var stored Grant
	if err := json.Unmarshal(buf.Bytes(), &stored); err != nil {
		t.Fatalf("JSONL line does not parse: %v (%q)", err, buf.String())
	}
	if stored.Path != ".env" || stored.Approver != "terminal" {
		t.Fatalf("unexpected persisted grant: %+v", stored)
	}
}

func TestCheckHonoursTTL(t *testing.T) {
	var buf bytes.Buffer
	s := newTestStore(t, &buf)
	if err := s.Add(Grant{Path: ".env", TTL: "30m"}); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Check(".env", fixedNow().Add(time.Minute)); !ok {
		t.Fatal("grant must be active within TTL")
	}
	if _, ok := s.Check(".env", fixedNow().Add(31*time.Minute)); ok {
		t.Fatal("grant must be expired after TTL")
	}
}

func TestDirectoryGrantCoversChildren(t *testing.T) {
	var buf bytes.Buffer
	s := newTestStore(t, &buf)
	if err := s.Add(Grant{Path: ".ssh", TTL: "30m"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Check(".ssh/id_ed25519", fixedNow()); !ok {
		t.Fatal("directory grant must cover children")
	}
	if _, ok := s.Check(".ssh", fixedNow()); !ok {
		t.Fatal("directory grant must cover itself")
	}
	if _, ok := s.Check(".sshx", fixedNow()); ok {
		t.Fatal("prefix must not leak past the slash boundary")
	}
}

func TestClosestDirectoryGrantWins(t *testing.T) {
	s := newTestStore(t, nil)
	s.Add(Grant{Path: "certs", TTL: "1h"})
	s.Add(Grant{Path: "certs/internal", TTL: "1h"})
	g, ok := s.Check("certs/internal/ca.pem", fixedNow())
	if !ok || g.Path != "certs/internal" {
		t.Fatalf("want closest grant certs/internal, got %+v ok=%v", g, ok)
	}
}

func TestExpiredGrantDoesNotMatch(t *testing.T) {
	s := newTestStore(t, nil)
	s.Add(Grant{Path: ".env", TTL: "1m", GrantedAt: fixedNow(), ExpiresAt: fixedNow().Add(time.Minute)})
	if _, ok := s.Check(".env", fixedNow().Add(2*time.Minute)); ok {
		t.Fatal("expired grant must not cover its path")
	}
}

func TestActiveSorted(t *testing.T) {
	s := newTestStore(t, nil)
	s.Add(Grant{Path: "b.txt", TTL: "1h"})
	s.Add(Grant{Path: "a.txt", TTL: "1h"})
	got := s.Active(fixedNow())
	if len(got) != 2 || got[0].Path != "a.txt" || got[1].Path != "b.txt" {
		t.Fatalf("Active not sorted: %+v", got)
	}
}

func TestCoversNormalization(t *testing.T) {
	g := Grant{Path: ".env"}
	for _, p := range []string{".env", "/.env", "./.env"} {
		if !g.Covers(p) {
			t.Errorf("grant must cover %q", p)
		}
	}
	if g.Covers(".env.local") {
		t.Error("grant must not cover sibling names")
	}
}
