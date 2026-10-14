package manifest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func at(min int) time.Time { return time.Date(2026, 10, 15, 9, min, 0, 0, time.UTC) }

func header() Header {
	return Header{
		RunID:        "20261015-090000-deadbe",
		AgentCmd:     []string{"mockagent"},
		ModelTarget:  "deepseek-v4.1-flash",
		PolicyDigest: "sha256:ab12",
		EnvVarNames:  []string{"HOME", "PATH", "READSCOPE_RUN_ID"},
		StartedAt:    at(0),
		EndedAt:      at(5),
	}
}

func TestFromDecisionsLastVerdictWins(t *testing.T) {
	decs := []Decision{
		{At: at(1), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictDenied},
		{At: at(2), Op: "stat", Path: ".env", Verdict: VerdictDenied},
		{At: at(3), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictGranted},
		{At: at(3), Op: "open", Verb: "read", Path: "src/main.go", Verdict: VerdictAllowed},
		{At: at(4), Op: "open", Verb: "write", Path: "src/new.go", Verdict: VerdictAllowed},
	}
	m := FromDecisions(header(), decs)

	if len(m.Reads) != 2 {
		t.Fatalf("want 2 read records (writes are not reads), got %d: %+v", len(m.Reads), m.Reads)
	}
	if m.Reads[0].Path != ".env" || m.Reads[0].Verdict != VerdictGranted || !m.Reads[0].At.Equal(at(3)) {
		t.Errorf("last verdict must win with its timestamp: %+v", m.Reads[0])
	}
	if m.Reads[1].Path != "src/main.go" || m.Reads[1].Verdict != VerdictAllowed {
		t.Errorf("second read record wrong: %+v", m.Reads[1])
	}
}

func TestReplayDeterministic(t *testing.T) {
	decs := []Decision{
		{At: at(1), Op: "open", Verb: "read", Path: "b.txt", Verdict: VerdictAllowed},
		{At: at(1), Op: "open", Verb: "read", Path: "a.txt", Verdict: VerdictAllowed},
		{At: at(2), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictDenied},
	}
	m1 := FromDecisions(header(), decs)
	m2 := FromDecisions(header(), decs)
	a, _ := json.Marshal(m1)
	b, _ := json.Marshal(m2)
	if !bytes.Equal(a, b) {
		t.Fatal("replaying the same decision log must reproduce the manifest byte for byte")
	}
}

func TestLogAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions.jsonl")
	l, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	decs := []Decision{
		{At: at(1), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictDenied},
		{At: at(2), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictGranted},
	}
	for _, d := range decs {
		if err := l.Append(d); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	back, err := ReadDecisions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[1].Verdict != VerdictGranted {
		t.Fatalf("replayed decisions wrong: %+v", back)
	}

	m := FromDecisions(header(), back)
	if m.Reads[0].Verdict != VerdictGranted {
		t.Fatalf("replay must reproduce the granted verdict, got %+v", m.Reads)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	h := header()
	decs := []Decision{
		{At: at(1), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictDenied},
		{At: at(2), Op: "open", Verb: "read", Path: ".env", Verdict: VerdictGranted},
	}
	m := FromDecisions(h, decs)
	m.SetGrants([]GrantRecord{{Path: ".env", TTL: "30m", Approver: "terminal", GrantedAt: at(2), ExpiresAt: at(32)}})

	path, err := m.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != FileName(h.RunID) {
		t.Fatalf("unexpected manifest name %s", path)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.Schema != Schema || back.RunID != h.RunID || len(back.Grants) != 1 || back.Grants[0].Path != ".env" {
		t.Fatalf("roundtrip mismatch: %+v", back)
	}
	if back.Reads[0].Verdict != VerdictGranted {
		t.Fatalf("reads wrong: %+v", back.Reads)
	}
}

func TestFindInDirPicksLatest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"exposure-manifest-20261015-090000-aaa.json",
		"exposure-manifest-20261015-091000-bbb.json",
		"other.json",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FindInDir(dir)
	if err != nil || filepath.Base(got) != "exposure-manifest-20261015-091000-bbb.json" {
		t.Fatalf("FindInDir = %s, %v", got, err)
	}
}
