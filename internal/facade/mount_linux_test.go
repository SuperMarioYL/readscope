//go:build linux

// Real-mount integration test: exercises the facade through the kernel.
// Skipped automatically when no FUSE device / fusermount is available
// (e.g. plain containers without --device /dev/fuse).
package facade

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SuperMarioYL/readscope/internal/manifest"
)

func fuseAvailable(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skipf("no /dev/fuse: %v", err)
	}
	if _, err := exec.LookPath("fusermount"); err != nil {
		if _, err3 := exec.LookPath("fusermount3"); err3 != nil {
			t.Skip("no fusermount available")
		}
	}
}

func mountFacade(t *testing.T, broker Broker) (*Facade, string, string) {
	t.Helper()
	fuseAvailable(t)

	f, src, _ := newTestFacade(t, broker)
	mnt := filepath.Join(t.TempDir(), "view")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	server, err := f.Mount(mnt)
	if err != nil {
		t.Skipf("mount failed (no fuse privilege?): %v", err)
	}
	t.Cleanup(func() {
		server.Unmount()
	})
	return f, src, mnt
}

func TestMountDefaultDeny(t *testing.T) {
	_, src, mnt := mountFacade(t, &recordingBroker{})

	// Allowed source is readable through the facade.
	data, err := os.ReadFile(filepath.Join(mnt, "src", "main.go"))
	if err != nil {
		t.Fatalf("allowed read failed: %v", err)
	}
	if string(data) != "package main\n" {
		t.Fatalf("allowed read content wrong: %q", data)
	}

	// Denied path: EACCES on open, and the backing file is untouched.
	if _, err := os.ReadFile(filepath.Join(mnt, ".env")); !os.IsPermission(err) {
		t.Fatalf("denied read should be EACCES, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(mnt, ".env")); !os.IsPermission(err) {
		t.Fatalf("denied stat should be EACCES, got %v", err)
	}

	// The control: the same file through the backing path is readable,
	// proving the refusal happens in the facade, not the filesystem.
	if _, err := os.ReadFile(filepath.Join(src, ".env")); err != nil {
		t.Fatalf("backing read must succeed: %v", err)
	}

	// Denied paths are absent from listings.
	entries, err := os.ReadDir(mnt)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == ".env" {
			t.Error("denied entry must not appear in listings")
		}
	}
	foundSrc := false
	for _, e := range entries {
		if e.Name() == "src" {
			foundSrc = true
		}
	}
	if !foundSrc {
		t.Error("allowed entries must appear in listings")
	}
}

func TestMountWriteBoundary(t *testing.T) {
	_, _, mnt := mountFacade(t, &recordingBroker{})

	// Allowed path: writes pass through.
	if err := os.WriteFile(filepath.Join(mnt, "src", "new.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatalf("allowed write failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mnt, "src", "new.go")); err != nil {
		t.Fatalf("written file must exist: %v", err)
	}

	// Denied path: write refused, nothing created.
	if err := os.WriteFile(filepath.Join(mnt, "env.backup"), []byte("x"), 0o600); !os.IsPermission(err) {
		t.Fatalf("denied write should be EACCES, got %v", err)
	}
}

func TestMountInlineGrantFlow(t *testing.T) {
	broker := &recordingBroker{answer: true}
	_, _, mnt := mountFacade(t, broker)

	// The first open blocks on the inline approval; the broker records
	// the grant and the same open succeeds - the m2 inline flow.
	data, err := os.ReadFile(filepath.Join(mnt, ".env"))
	if err != nil {
		t.Fatalf("granted read failed: %v", err)
	}
	if !strings.Contains(string(data), "SECRET=1") {
		t.Fatalf("granted read content wrong: %q", data)
	}
	if len(broker.asked) != 1 || broker.asked[0] != ".env" {
		t.Fatalf("broker asked wrong: %v", broker.asked)
	}

	// Writes stay refused even with the read grant active.
	if err := os.WriteFile(filepath.Join(mnt, ".env"), []byte("x"), 0o600); !os.IsPermission(err) {
		t.Fatalf("write to granted path must stay EACCES, got %v", err)
	}
}

func TestMountInlineGrantRefused(t *testing.T) {
	broker := &recordingBroker{answer: false}
	_, _, mnt := mountFacade(t, broker)

	if _, err := os.ReadFile(filepath.Join(mnt, ".env")); !os.IsPermission(err) {
		t.Fatalf("refused read should be EACCES, got %v", err)
	}
	if len(broker.asked) != 1 {
		t.Fatalf("broker must be asked exactly once per path, got %v", broker.asked)
	}
}

func TestMountDecisionLog(t *testing.T) {
	dir := t.TempDir()
	_, _, mnt := newMountedWithLog(t, dir)

	if _, err := os.ReadFile(filepath.Join(mnt, "src", "main.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(mnt, ".env")); !os.IsPermission(err) {
		t.Fatalf("want EACCES, got %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"path":"src/main.go"`) || !strings.Contains(text, `"verdict":"allowed"`) {
		t.Errorf("decision log missing allowed read: %s", text)
	}
	if !strings.Contains(text, `"path":".env"`) || !strings.Contains(text, `"verdict":"denied"`) {
		t.Errorf("decision log missing denied read: %s", text)
	}
}

func newMountedWithLog(t *testing.T, logDir string) (*Facade, string, string) {
	t.Helper()
	fuseAvailable(t)
	f, src, _ := newTestFacade(t, &recordingBroker{})
	log, err := manifest.OpenLog(filepath.Join(logDir, "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	f.opts.Log = log
	mnt := filepath.Join(t.TempDir(), "view")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		t.Fatal(err)
	}
	server, err := f.Mount(mnt)
	if err != nil {
		t.Skipf("mount failed: %v", err)
	}
	t.Cleanup(func() { server.Unmount() })
	return f, src, mnt
}
