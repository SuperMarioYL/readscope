package inspect

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SuperMarioYL/readscope/internal/manifest"
)

func buildManifest(t *testing.T, dir string, withLog bool) (*manifest.Manifest, string) {
	t.Helper()
	runID := "20261015-090000-a1b2c3"
	started := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	h := manifest.Header{
		RunID:        runID,
		AgentCmd:     []string{"mockagent"},
		ModelTarget:  "deepseek-v4.1-flash",
		PolicyDigest: "sha256:abcd1234",
		EnvVarNames:  []string{"HOME", "PATH", "READSCOPE_RUN_ID"},
		StartedAt:    started,
		EndedAt:      started.Add(65 * time.Second),
	}
	decs := []manifest.Decision{
		{At: started.Add(2 * time.Second), Op: "open", Verb: "read", Path: "src/main.go", Verdict: manifest.VerdictAllowed},
		{At: started.Add(4 * time.Second), Op: "open", Verb: "read", Path: ".env", Verdict: manifest.VerdictDenied},
		{At: started.Add(5 * time.Second), Op: "open", Verb: "read", Path: ".env", Verdict: manifest.VerdictGranted},
	}
	if withLog {
		log, err := manifest.OpenLog(filepath.Join(dir, "decisions.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range decs {
			if err := log.Append(d); err != nil {
				t.Fatal(err)
			}
		}
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest.FromDecisions(h, decs)
	m.SetGrants([]manifest.GrantRecord{{
		Path:      ".env",
		TTL:       "30m",
		GrantedAt: started.Add(5 * time.Second),
		ExpiresAt: started.Add(35 * time.Minute),
		Approver:  "terminal",
		Reason:    "approved in terminal",
	}})
	path, err := m.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &m, path
}

func TestLoadFromDirAndFile(t *testing.T) {
	dir := t.TempDir()
	m, file := buildManifest(t, dir, false)

	fromDir, path, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(dir): %v", err)
	}
	if path != file || fromDir.RunID != m.RunID {
		t.Fatalf("Load(dir) = %s, %s", path, fromDir.RunID)
	}
	fromFile, path2, err := Load(file)
	if err != nil || path2 != file || fromFile.RunID != m.RunID {
		t.Fatalf("Load(file) = %s, %v", path2, err)
	}
}

func TestVerifyReplayMatches(t *testing.T) {
	dir := t.TempDir()
	m, file := buildManifest(t, dir, true)

	res, err := VerifyReplay(m, file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.LogPath, "decisions.jsonl") {
		t.Fatalf("log path = %s", res.LogPath)
	}
	if !res.Verified || !res.Match {
		t.Fatal("replaying the decision log must reproduce the manifest")
	}
}

func TestVerifyReplayDetectsTampering(t *testing.T) {
	dir := t.TempDir()
	m, file := buildManifest(t, dir, true)

	// Tamper with the manifest: claim .env was never read.
	m.Reads = m.Reads[:0]
	res, err := VerifyReplay(m, file)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified {
		t.Fatal("a decision log was present and must be verified")
	}
	if res.Match {
		t.Fatal("a tampered manifest must fail replay verification")
	}
}

func TestVerifyReplayWithoutLog(t *testing.T) {
	dir := t.TempDir()
	m, file := buildManifest(t, dir, false)

	res, err := VerifyReplay(m, file)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verified || res.Match {
		t.Fatal("without a decision log there is nothing to verify")
	}
}

func TestSummarizeContainsFacts(t *testing.T) {
	dir := t.TempDir()
	m, file := buildManifest(t, dir, true)
	res, err := VerifyReplay(m, file)
	if err != nil {
		t.Fatal(err)
	}

	text := Summarize(m, file, &res)
	for _, want := range []string{
		"暴露清单摘要",
		"20261015-090000-a1b2c3",
		"deepseek-v4.1-flash",
		"允许 1 · 授权 1 · 拒绝 0",
		"授权读取   .env",
		"允许读取   src/main.go",
		".env  TTL 30m  审批人 terminal",
		"3 个名字",
		"重放校验     判定日志重放与清单一致",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET") {
		t.Error("summary must not contain file contents")
	}
}

func TestSummarizeDenied(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	h := manifest.Header{RunID: "r2", AgentCmd: []string{"agent"}, StartedAt: started, EndedAt: started}
	decs := []manifest.Decision{
		{At: started, Op: "open", Verb: "read", Path: ".ssh/id_rsa", Verdict: manifest.VerdictDenied},
	}
	m := manifest.FromDecisions(h, decs)
	text := Summarize(&m, filepath.Join(dir, "m.json"), nil)
	if !strings.Contains(text, "拒绝 1") || !strings.Contains(text, ".ssh/id_rsa") {
		t.Errorf("denied summary wrong:\n%s", text)
	}
	if !strings.Contains(text, "限时授权     无") {
		t.Errorf("empty grants line wrong:\n%s", text)
	}
}

func TestLoadMissing(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("missing manifest must error")
	}
	empty := t.TempDir()
	if _, _, err := Load(empty); err == nil {
		t.Fatal("empty run dir must error")
	}
}
