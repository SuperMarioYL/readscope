package facade

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/SuperMarioYL/readscope/internal/grant"
	"github.com/SuperMarioYL/readscope/internal/policy"
)

const testPolicy = `
default: deny
rules:
  - glob: "src/**"
    verbs: [read, write]
  - glob: "*.md"
    verbs: [read]
`

type recordingBroker struct {
	asked  []string
	answer bool
	// store, when set, receives the grant on approval, mirroring the
	// runner's inline approval flow.
	store *grant.Store
}

func (b *recordingBroker) RequestGrant(rel string) bool {
	b.asked = append(b.asked, rel)
	if !b.answer || b.store == nil {
		return false
	}
	now := time.Now()
	return b.store.Add(grant.Grant{
		Path:      rel,
		TTL:       "30m",
		GrantedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
		Approver:  "terminal",
	}) == nil
}

func newTestFacade(t *testing.T, broker Broker) (*Facade, string, *recordingBroker) {
	t.Helper()
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := policy.Load(writePolicy(t, testPolicy))
	if err != nil {
		t.Fatal(err)
	}
	store := grant.NewStore(nil)
	f, err := New(Options{Source: src, Policy: p, Grants: store, Broker: broker, Now: time.Now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if rb, ok := broker.(*recordingBroker); ok {
		rb.store = store
		return f, src, rb
	}
	return f, src, nil
}

func writePolicy(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNewValidation(t *testing.T) {
	dir := t.TempDir()
	p, err := policy.Load(writePolicy(t, testPolicy))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Source: dir, Grants: grant.NewStore(nil)}); err == nil {
		t.Error("missing policy must be rejected")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Source: file, Policy: p, Grants: grant.NewStore(nil)}); err == nil {
		t.Error("non-directory source must be rejected")
	}
	if _, err := New(Options{Source: filepath.Join(dir, "missing"), Policy: p, Grants: grant.NewStore(nil)}); err == nil {
		t.Error("missing source must be rejected")
	}
}

func TestOpenIntent(t *testing.T) {
	cases := []struct {
		flags     uint32
		wantRead  bool
		wantWrite bool
	}{
		{syscall.O_RDONLY, true, false},
		{syscall.O_WRONLY, false, true},
		{syscall.O_RDWR, true, true},
		{syscall.O_RDONLY | syscall.O_CREAT, true, false},
	}
	for _, c := range cases {
		r, w := openIntent(c.flags)
		if r != c.wantRead || w != c.wantWrite {
			t.Errorf("openIntent(flags=%d) = %v,%v; want %v,%v", c.flags, r, w, c.wantRead, c.wantWrite)
		}
	}
}

func TestVisible(t *testing.T) {
	f, _, _ := newTestFacade(t, &recordingBroker{})

	if !f.visible("src/main.go") {
		t.Error("policy-allowed path must be visible")
	}
	if f.visible(".env") {
		t.Error("denied path must not be visible without a grant")
	}
	if !f.visible("") {
		t.Error("the workspace root itself is always visible")
	}
	if err := f.opts.Grants.Add(grant.Grant{Path: ".env", TTL: "1h"}); err != nil {
		t.Fatal(err)
	}
	if !f.visible(".env") {
		t.Error("granted path must be visible")
	}
}

func TestTypeOnlyAttrHidesMetadata(t *testing.T) {
	var st syscall.Stat_t
	path := filepath.Join(t.TempDir(), "secret.env")
	if err := os.WriteFile(path, []byte("VERY=SECRET-LONG-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	a := typeOnlyAttr(&st)
	if a.Mode&syscall.S_IFMT != syscall.S_IFREG {
		t.Errorf("type must be preserved, got mode %o", a.Mode)
	}
	if a.Size != 0 || a.Mtime != 0 || a.Uid != 0 || a.Gid != 0 {
		t.Errorf("metadata must be withheld: %+v", a)
	}
	if a.Nlink != 1 || a.Blksize != 4096 {
		t.Errorf("placeholder fields unexpected: %+v", a)
	}
}

func TestJoinRelRejectsEscape(t *testing.T) {
	f, _, _ := newTestFacade(t, &recordingBroker{})
	if got := f.joinRel("", "ok.txt"); got != "ok.txt" {
		t.Errorf("joinRel root = %q", got)
	}
	if got := f.joinRel("src", "a.go"); got != "src/a.go" {
		t.Errorf("joinRel nested = %q", got)
	}
	if got := f.joinRel("", ".."); got != "\x00escaped" {
		t.Errorf("joinRel must reject escape, got %q", got)
	}
}

func TestGrantExpiryRehides(t *testing.T) {
	f, _, _ := newTestFacade(t, &recordingBroker{})
	base := time.Now()
	clock := base
	f.opts.Now = func() time.Time { return clock }

	if err := f.opts.Grants.Add(grant.Grant{
		Path:      ".env",
		TTL:       "10m",
		GrantedAt: base,
		ExpiresAt: base.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if !f.visible(".env") {
		t.Fatal("granted path must be visible")
	}
	clock = base.Add(11 * time.Minute)
	if f.visible(".env") {
		t.Error("expired grant must re-hide the path")
	}
}
