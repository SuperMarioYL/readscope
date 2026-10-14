package runner

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/SuperMarioYL/readscope/internal/grant"
)

func brokerStore(t *testing.T) *grant.Store {
	t.Helper()
	return grant.NewStore(nil)
}

func TestBrokerApprovalRecordsGrant(t *testing.T) {
	store := brokerStore(t)
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store:       store,
		TTL:         30 * time.Minute,
		Interactive: true,
		In:          strings.NewReader("y\n"),
		Out:         out,
	})

	if !b.RequestGrant(".env") {
		t.Fatal("approval must return true")
	}
	text := out.String()
	if !strings.Contains(text, "Agent 请求读取被拒路径: .env") {
		t.Errorf("prompt missing: %q", text)
	}
	if !strings.Contains(text, "已授权 .env") {
		t.Errorf("confirmation missing: %q", text)
	}
	if len(store.Active(time.Now())) != 1 {
		t.Fatal("grant must be recorded in the store")
	}
}

func TestBrokerRejectionDenies(t *testing.T) {
	store := brokerStore(t)
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store:       store,
		TTL:         30 * time.Minute,
		Interactive: true,
		In:          strings.NewReader("n\n"),
		Out:         out,
	})

	if b.RequestGrant(".env") {
		t.Fatal("rejection must return false")
	}
	if !strings.Contains(out.String(), "已拒绝 .env") {
		t.Errorf("denial note missing: %q", out.String())
	}
	if len(store.Active(time.Now())) != 0 {
		t.Fatal("no grant may be recorded on rejection")
	}
}

func TestBrokerAskedOncePerPath(t *testing.T) {
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store:       brokerStore(t),
		TTL:         time.Minute,
		Interactive: true,
		In:          strings.NewReader("n\n"),
		Out:         out,
	})

	if b.RequestGrant(".env") {
		t.Fatal("first answer is n")
	}
	if b.RequestGrant(".env") {
		t.Fatal("second ask must be auto-denied")
	}
	if got := strings.Count(out.String(), "y/N"); got != 1 {
		t.Fatalf("path must be asked exactly once, prompt count = %d", got)
	}
}

func TestBrokerNonInteractiveDenies(t *testing.T) {
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store: brokerStore(t),
		TTL:   time.Minute,
		In:    strings.NewReader("y\n"),
		Out:   out,
	})

	if b.RequestGrant(".env") {
		t.Fatal("non-interactive mode must deny")
	}
	if !strings.Contains(out.String(), "非交互模式") {
		t.Errorf("non-interactive note missing: %q", out.String())
	}
}

func TestBrokerEOFDenies(t *testing.T) {
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store:       brokerStore(t),
		TTL:         time.Minute,
		Interactive: true,
		In:          strings.NewReader(""),
		Out:         out,
	})

	if b.RequestGrant(".env") {
		t.Fatal("EOF must deny")
	}
	if !strings.Contains(out.String(), "未收到答复") {
		t.Errorf("EOF note missing: %q", out.String())
	}
}

func TestBrokerGrantExpiryWindow(t *testing.T) {
	store := brokerStore(t)
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	out := &bytes.Buffer{}
	b := NewBroker(BrokerConfig{
		Store:       store,
		TTL:         30 * time.Minute,
		Interactive: true,
		In:          strings.NewReader("y\n"),
		Out:         out,
		Now:         func() time.Time { return base },
	})

	if !b.RequestGrant(".env") {
		t.Fatal("approval must return true")
	}
	active := store.Active(base)
	if len(active) != 1 || !active[0].ExpiresAt.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("grant window wrong: %+v", active)
	}
	if _, ok := store.Check(".env", base.Add(31*time.Minute)); ok {
		t.Fatal("grant must be expired after TTL")
	}
}

func TestEnvNamesDedupSorted(t *testing.T) {
	env := []string{"PATH=/x", "HOME=/", "PATH=/y", "READSCOPE_RUN_ID=r1", "novalue"}
	got := EnvNames(env)
	want := []string{"HOME", "PATH", "READSCOPE_RUN_ID"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("EnvNames = %v, want %v", got, want)
	}
}
