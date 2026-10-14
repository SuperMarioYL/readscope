package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePolicy = `# test policy
default: deny
rules:
  - glob: ".env*"
    explain: "dotenv secrets"
  - glob: "src/**"
    verbs: [read, write]
  - glob: "**/*.go"
    verbs: [read, write]
  - glob: "*.md"
    verbs: [read]
  - glob: "*.pem"
  - glob: ".ssh/**"
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadSample(t *testing.T) *Policy {
	t.Helper()
	p, err := Load(writeTemp(t, samplePolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return p
}

func TestDecide(t *testing.T) {
	p := loadSample(t)

	cases := []struct {
		path  string
		verb  Verb
		want  Verdict
		label string
	}{
		{"src/main.go", Read, Allow, "source readable"},
		{"src/main.go", Write, Allow, "source writable"},
		{"cmd/readscope/main.go", Write, Allow, "**/*.go covers nested source"},
		{"README.md", Read, Allow, "docs readable"},
		{"README.md", Write, Deny, "docs not writable"},
		{".env", Read, Deny, "explicit empty-verb rule denies"},
		{".env", Write, Deny, "explicit empty-verb rule denies writes"},
		{"config/.env.local", Read, Deny, "basename glob matches any depth"},
		{"certs/server.pem", Read, Deny, "pem denied at depth"},
		{".ssh/id_ed25519", Read, Deny, "ssh dir denied"},
		{".ssh", Read, Deny, "ssh dir itself denied"},
		{"go.sum", Read, Deny, "unmatched path falls through to default deny"},
	}
	for _, c := range cases {
		if got := p.Decide(c.path, c.verb); got != c.want {
			t.Errorf("%s: Decide(%q, %s) = %v, want %v", c.label, c.path, c.verb, got, c.want)
		}
	}
}

func TestFirstMatchWins(t *testing.T) {
	p := loadSample(t)
	// "src/**" comes before "*.pem", so source-tree pem files follow the
	// source rule; elsewhere the pem rule denies.
	if got := p.Decide("src/keys.pem", Write); got != Allow {
		t.Errorf("first-match-wins broken: src/keys.pem write = %v", got)
	}
	if got := p.Decide("certs/keys.pem", Write); got != Deny {
		t.Errorf("certs/keys.pem write = %v, want deny", got)
	}
}

func TestLoadRejectsNonDenyDefault(t *testing.T) {
	_, err := Load(writeTemp(t, "default: allow\nrules:\n  - glob: \"*\"\n"))
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("want default-deny validation error, got %v", err)
	}
}

func TestLoadRejectsUnknownVerb(t *testing.T) {
	_, err := Load(writeTemp(t, "default: deny\nrules:\n  - glob: \"*\"\n    verbs: [execute]\n"))
	if err == nil || !strings.Contains(err.Error(), "verb") {
		t.Fatalf("want unknown-verb error, got %v", err)
	}
}

func TestLoadRejectsDuplicateGlob(t *testing.T) {
	_, err := Load(writeTemp(t, "default: deny\nrules:\n  - glob: \"*\"\n  - glob: \"*\"\n    verbs: [read]\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate-glob error, got %v", err)
	}
}

func TestDigestStableAndSensitive(t *testing.T) {
	a := loadSample(t)
	b := loadSample(t)
	if a.Digest() != b.Digest() {
		t.Fatal("digest must be stable for identical files")
	}
	if !strings.HasPrefix(a.Digest(), "sha256:") || len(a.Digest()) != len("sha256:")+64 {
		t.Fatalf("digest format unexpected: %s", a.Digest())
	}
	c, err := Load(writeTemp(t, samplePolicy + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Digest() == a.Digest() {
		t.Fatal("digest must change when the file changes")
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{".env", ".env"},
		{"./src/../src/main.go", "src/main.go"},
		{"/abs/like.txt", "abs/like.txt"},
		{"src//main.go", "src/main.go"},
	}
	for _, c := range cases {
		got, ok := Normalize(c.in)
		if !ok || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
	if _, ok := Normalize("../escape.txt"); ok {
		t.Error("paths escaping the workspace must be rejected")
	}
}

func TestGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"src/**", "src/a.go", true},
		{"src/**", "src/a/b/c.go", true},
		{"src/**", "src", false},
		{"src/**", "other/a.go", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"*.go", "main.go", true},
		{"*.go", "cmd/main.go", true},
		{"*.go", "main.go.bak", false},
		{"main?.go", "main1.go", true},
		{"main?.go", "main12.go", false},
		{"**", "anything/at/all", true},
	}
	for _, c := range cases {
		m, err := compileGlob(c.pattern)
		if err != nil {
			t.Fatalf("compileGlob(%q): %v", c.pattern, err)
		}
		if got := m.match(c.path); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}
