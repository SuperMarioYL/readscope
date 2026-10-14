// Package manifest records every policy decision of a run and derives
// the per-run exposure manifest from that decision log.
//
// The manifest is never assembled from memory state: it is produced by
// replaying decisions.jsonl, the append-only log the facade writes in
// real time. That makes the artifact replay-verifiable - the same log
// always yields the same manifest - which is what turns it into an
// auditable record of what a Coding Agent run could read.
package manifest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Schema identifies the manifest file format. v0 tracks the format
// documented in schemas/exposure-manifest.v0.json.
const Schema = "readscope.exposure-manifest/v0"

// Verdict values recorded per read.
const (
	VerdictAllowed = "allowed"
	VerdictGranted = "granted"
	VerdictDenied  = "denied"
)

// Decision is one policy decision the facade made, in workspace-relative
// terms. Op is one of "open", "stat", "list", "write", "other"; Verb is
// "read" or "write" (only meaningful for open).
type Decision struct {
	At      time.Time `json:"at"`
	Op      string    `json:"op"`
	Verb    string    `json:"verb,omitempty"`
	Path    string    `json:"path"`
	Verdict string    `json:"verdict"`
}

// ReadRecord is the per-path read outcome surfaced in the manifest: the
// last verdict a path received during the run.
type ReadRecord struct {
	Path    string    `json:"path"`
	Verdict string    `json:"verdict"`
	At      time.Time `json:"at"`
}

// Header carries the facts recorded at run start and end.
type Header struct {
	RunID        string
	AgentCmd     []string
	ModelTarget  string
	PolicyDigest string
	EnvVarNames  []string
	StartedAt    time.Time
	EndedAt      time.Time
}

// Manifest is the per-run exposure artifact written at run end.
type Manifest struct {
	Schema       string        `json:"schema"`
	RunID        string        `json:"run_id"`
	AgentCmd     []string      `json:"agent_cmd"`
	ModelTarget  string        `json:"model_target,omitempty"`
	PolicyDigest string        `json:"policy_digest"`
	EnvVarNames  []string      `json:"env_var_names"`
	Reads        []ReadRecord  `json:"reads"`
	Grants       []GrantRecord `json:"grants"`
	StartedAt    time.Time     `json:"started_at"`
	EndedAt      time.Time     `json:"ended_at"`
}

// GrantRecord mirrors the persisted grant fields. It is declared here
// instead of importing internal/grant so the manifest format stays a
// self-contained contract.
type GrantRecord struct {
	Path      string    `json:"path"`
	TTL       string    `json:"ttl"`
	GrantedAt time.Time `json:"granted_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Approver  string    `json:"approver"`
	Reason    string    `json:"reason"`
}

// Log is the append-only decision log (decisions.jsonl).
type Log struct {
	mu sync.Mutex
	f  *os.File
}

// OpenLog creates (or truncates) the decision log for a run.
func OpenLog(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open decision log: %w", err)
	}
	return &Log{f: f}, nil
}

// Append writes one decision as a JSON line, flushed immediately so a
// crashed run still leaves a usable audit trail.
func (l *Log) Append(d Decision) error {
	line, err := json.Marshal(d)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return err
	}
	return l.f.Sync()
}

// Close flushes and closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// ReadDecisions loads a decision log for replay.
func ReadDecisions(path string) ([]Decision, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open decision log: %w", err)
	}
	defer f.Close()

	var out []Decision
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var d Decision
		if err := json.Unmarshal(line, &d); err != nil {
			return nil, fmt.Errorf("decision log %s: corrupt line: %w", path, err)
		}
		out = append(out, d)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("decision log %s: %w", path, err)
	}
	return out, nil
}

// FromDecisions replays a decision log into a manifest: one ReadRecord
// per unique path (last verdict wins, timestamped when that verdict was
// recorded), grants sorted by path, deterministic ordering throughout.
// Reads surface open-for-read decisions; other operations stay in the
// decision log only.
func FromDecisions(h Header, decisions []Decision) Manifest {
	last := map[string]ReadRecord{}
	order := []string{}
	for _, d := range decisions {
		if d.Op != "open" || d.Verb != "read" {
			continue
		}
		if _, seen := last[d.Path]; !seen {
			order = append(order, d.Path)
		}
		last[d.Path] = ReadRecord{Path: d.Path, Verdict: d.Verdict, At: d.At}
	}

	reads := make([]ReadRecord, 0, len(order))
	for _, p := range order {
		reads = append(reads, last[p])
	}
	sort.SliceStable(reads, func(i, j int) bool {
		if !reads[i].At.Equal(reads[j].At) {
			return reads[i].At.Before(reads[j].At)
		}
		return reads[i].Path < reads[j].Path
	})

	return Manifest{
		Schema:       Schema,
		RunID:        h.RunID,
		AgentCmd:     h.AgentCmd,
		ModelTarget:  h.ModelTarget,
		PolicyDigest: h.PolicyDigest,
		EnvVarNames:  h.EnvVarNames,
		Reads:        reads,
		StartedAt:    h.StartedAt,
		EndedAt:      h.EndedAt,
	}
}

// SetGrants attaches the run's grants to the manifest, sorted by path
// for deterministic output.
func (m *Manifest) SetGrants(grants []GrantRecord) {
	sorted := append([]GrantRecord(nil), grants...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	m.Grants = sorted
}

// FileName returns the canonical manifest file name for a run.
func FileName(runID string) string {
	return "exposure-manifest-" + runID + ".json"
}

// Save writes the manifest as indented JSON.
func (m *Manifest) Save(dir string) (string, error) {
	path := filepath.Join(dir, FileName(m.RunID))
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	return path, nil
}

// Load reads a manifest file.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// FindInDir locates the exposure manifest inside a run directory. A
// runs root also works: the newest run directory containing a manifest
// wins, so `readscope inspect <runs-dir>` summarizes the latest run.
func FindInDir(dir string) (string, error) {
	direct, err := filepath.Glob(filepath.Join(dir, "exposure-manifest-*.json"))
	if err != nil {
		return "", err
	}
	if len(direct) > 0 {
		sort.Strings(direct)
		return direct[len(direct)-1], nil
	}
	nested, err := filepath.Glob(filepath.Join(dir, "*", "exposure-manifest-*.json"))
	if err != nil {
		return "", err
	}
	if len(nested) == 0 {
		return "", fmt.Errorf("no exposure-manifest-*.json in %s", dir)
	}
	sort.Strings(nested)
	return nested[len(nested)-1], nil
}
