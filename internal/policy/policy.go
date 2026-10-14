// Package policy loads ReadScope workspace policies.
//
// A policy is default-deny: each rule lists the verbs it allows for paths
// matching its glob, the first matching rule wins, and every path that no
// rule matches is refused. This keeps the read/write surface of a Coding
// Agent run explicit instead of implicit.
//
// Glob semantics:
//   - a pattern containing "/" matches the workspace-relative path, where
//     "**" crosses directory separators and "*" does not;
//   - a pattern without "/" matches the base name at any depth, so
//     "*.pem" also covers "certs/server.pem".
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Verb is an operation a rule can allow on a path.
type Verb string

const (
	Read  Verb = "read"
	Write Verb = "write"
)

// Verdict is the outcome of a policy decision.
type Verdict int

const (
	Deny Verdict = iota
	Allow
)

func (v Verdict) String() string {
	if v == Allow {
		return "allowed"
	}
	return "denied"
}

// Rule allows its verbs on paths matching Glob. An empty verb list marks
// the path as explicitly denied even if it would otherwise be allowed.
type Rule struct {
	Glob    string `yaml:"glob" json:"glob"`
	Verbs   []Verb `yaml:"verbs" json:"verbs"`
	Explain string `yaml:"explain,omitempty" json:"explain,omitempty"`

	matcher *globMatcher
}

// Policy is a default-deny workspace projection policy.
type Policy struct {
	Default string `yaml:"default" json:"default"`
	Rules   []Rule `yaml:"rules" json:"rules"`

	raw []byte
}

// Load reads and validates a policy file. v0.1 only supports
// default-deny policies; anything else is rejected at load time.
func Load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse policy %s: %w", path, err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	p.raw = raw
	return &p, nil
}

func (p *Policy) validate() error {
	if strings.TrimSpace(p.Default) != "deny" {
		return fmt.Errorf(`only "default: deny" is supported (got %q)`, p.Default)
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("policy has no rules; nothing would be readable")
	}
	seen := map[string]bool{}
	for i := range p.Rules {
		r := &p.Rules[i]
		if r.Glob == "" {
			return fmt.Errorf("rule %d: glob is required", i)
		}
		if seen[r.Glob] {
			return fmt.Errorf("rule %d: duplicate glob %q (first match already wins)", i, r.Glob)
		}
		seen[r.Glob] = true
		for _, v := range r.Verbs {
			if v != Read && v != Write {
				return fmt.Errorf("rule %d (%s): unknown verb %q", i, r.Glob, v)
			}
		}
		m, err := compileGlob(r.Glob)
		if err != nil {
			return fmt.Errorf("rule %d: %w", i, err)
		}
		r.matcher = m
	}
	return nil
}

// Digest returns a stable fingerprint of the policy file, recorded in
// every exposure manifest so a manifest can be tied to the exact policy
// that produced it.
func (p *Policy) Digest() string {
	sum := sha256.Sum256(p.raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Decide decides one verb on one workspace-relative path. Paths are
// cleaned and normalized before matching; paths escaping the workspace
// are always denied.
func (p *Policy) Decide(rel string, verb Verb) Verdict {
	normalized, ok := Normalize(rel)
	if !ok {
		return Deny
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		if !r.matcher.match(normalized) {
			continue
		}
		for _, v := range r.Verbs {
			if v == verb {
				return Allow
			}
		}
		return Deny
	}
	return Deny
}

// Normalize cleans a workspace-relative path: separators normalized,
// leading slashes and "." removed, and ".." cancelled against preceding
// parts. A ".." that would escape the workspace root is rejected.
func Normalize(rel string) (out string, ok bool) {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	var parts []string
	for _, part := range strings.Split(rel, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", false
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/"), true
}

// globMatcher matches one glob against a normalized relative path.
type globMatcher struct {
	re *regexp.Regexp
}

func (m *globMatcher) match(path string) bool { return m.re.MatchString(path) }

func compileGlob(pattern string) (*globMatcher, error) {
	if strings.Contains(pattern, "..") {
		return nil, fmt.Errorf("glob %q must not contain \"..\"", pattern)
	}
	src, err := globRegexp(pattern)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}
	return &globMatcher{re: re}, nil
}

// globRegexp translates a glob to an anchored regexp source. A pattern
// without "/" also matches any base name, so "*.pem" covers "a/b.pem"
// and a root-level "a.pem".
func globRegexp(pattern string) (string, error) {
	var b strings.Builder
	b.WriteString("^")
	i := 0
	for i < len(pattern) {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i += 2
				// Collapse a following slash so "src/**" matches
				// "src/a.go" but not "src" itself.
				if i < len(pattern) && pattern[i] == '/' {
					b.WriteString("/?")
					i++
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("$")
	src := b.String()
	if !strings.Contains(pattern, "/") {
		// Base-name pattern: allow any directory prefix, including none.
		src = strings.Replace(src, "^", "^(?s:.*/)?", 1)
	}
	return src, nil
}
