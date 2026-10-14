// Package inspect turns an exposure manifest into a human summary.
//
// The summary answers one question in one screen: what could this run
// read? It also verifies the manifest against the run's decision log
// when that log sits next to the artifact - replaying the log must
// reproduce the same manifest.
package inspect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SuperMarioYL/readscope/internal/manifest"
)

// Load resolves its argument to a manifest: either a manifest file
// itself or a run directory containing one.
func Load(arg string) (*manifest.Manifest, string, error) {
	abs, err := filepath.Abs(arg)
	if err != nil {
		return nil, "", err
	}
	if fi, statErr := os.Stat(abs); statErr == nil && fi.IsDir() {
		file, findErr := manifest.FindInDir(abs)
		if findErr != nil {
			return nil, "", findErr
		}
		abs = file
	}
	m, loadErr := manifest.Load(abs)
	if loadErr != nil {
		return nil, "", loadErr
	}
	return m, abs, nil
}

// ReplayResult is the outcome of replay-verifying one manifest.
type ReplayResult struct {
	// Verified is true when a decision log was found next to the
	// manifest and replayed.
	Verified bool
	// Match is true when replaying the log reproduces the manifest.
	Match bool
	// LogPath is the decision log used (or attempted).
	LogPath string
}

// VerifyReplay replays the decision log that sits next to the manifest
// and reports whether it reproduces the same manifest.
func VerifyReplay(m *manifest.Manifest, manifestPath string) (ReplayResult, error) {
	res := ReplayResult{LogPath: filepath.Join(filepath.Dir(manifestPath), "decisions.jsonl")}
	decs, err := manifest.ReadDecisions(res.LogPath)
	if err != nil {
		return res, nil // no log to verify against
	}
	res.Verified = true
	replayed := manifest.FromDecisions(manifest.Header{
		RunID:        m.RunID,
		AgentCmd:     m.AgentCmd,
		ModelTarget:  m.ModelTarget,
		PolicyDigest: m.PolicyDigest,
		EnvVarNames:  m.EnvVarNames,
		StartedAt:    m.StartedAt,
		EndedAt:      m.EndedAt,
	}, decs)
	replayed.SetGrants(m.Grants)
	res.Match = manifestsEqual(m, &replayed)
	return res, nil
}

func manifestsEqual(a, b *manifest.Manifest) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// Summarize renders the Chinese one-screen summary. replay, when
// verified, adds the replay-verification line.
func Summarize(m *manifest.Manifest, manifestPath string, replay *ReplayResult) string {
	var b strings.Builder

	dur := m.EndedAt.Sub(m.StartedAt).Round(time.Second)
	fmt.Fprintln(&b, "── 暴露清单摘要 ──────────────────────────────")
	fmt.Fprintf(&b, "运行 ID      %s\n", m.RunID)
	fmt.Fprintf(&b, "Agent 命令   %s\n", strings.Join(m.AgentCmd, " "))
	if m.ModelTarget != "" {
		fmt.Fprintf(&b, "模型目标     %s\n", m.ModelTarget)
	}
	fmt.Fprintf(&b, "策略摘要     %s\n", m.PolicyDigest)
	fmt.Fprintf(&b, "起止时间     %s → %s（历时 %s）\n",
		m.StartedAt.Format("2006-01-02 15:04:05"), m.EndedAt.Format("15:04:05"), dur)
	fmt.Fprintf(&b, "清单文件     %s\n", manifestPath)

	var allowed, granted, denied []manifest.ReadRecord
	for _, r := range m.Reads {
		switch r.Verdict {
		case manifest.VerdictAllowed:
			allowed = append(allowed, r)
		case manifest.VerdictGranted:
			granted = append(granted, r)
		default:
			denied = append(denied, r)
		}
	}
	sortByPath := func(rs []manifest.ReadRecord) {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
	}
	sortByPath(allowed)
	sortByPath(granted)
	sortByPath(denied)

	fmt.Fprintf(&b, "读取判定     共 %d 个路径：允许 %d · 授权 %d · 拒绝 %d\n",
		len(m.Reads), len(allowed), len(granted), len(denied))
	if len(allowed) > 0 {
		fmt.Fprintf(&b, "  允许读取   %s\n", joinPaths(allowed))
	}
	if len(granted) > 0 {
		fmt.Fprintf(&b, "  授权读取   %s\n", joinPaths(granted))
	}
	if len(denied) > 0 {
		fmt.Fprintf(&b, "  拒绝读取   %s\n", joinPaths(denied))
	}

	if len(m.Grants) > 0 {
		fmt.Fprintf(&b, "限时授权     %d 条\n", len(m.Grants))
		for _, g := range m.Grants {
			fmt.Fprintf(&b, "  %s  TTL %s  审批人 %s  授予 %s\n",
				g.Path, g.TTL, g.Approver, g.GrantedAt.Format("15:04:05"))
		}
	} else {
		fmt.Fprintln(&b, "限时授权     无")
	}

	if len(m.EnvVarNames) > 0 {
		fmt.Fprintf(&b, "环境变量     %d 个名字（仅记录名字，不记录值）: %s\n",
			len(m.EnvVarNames), strings.Join(m.EnvVarNames, ", "))
	}

	if replay != nil && replay.Verified {
		if replay.Match {
			fmt.Fprintln(&b, "重放校验     判定日志重放与清单一致")
		} else {
			fmt.Fprintln(&b, "重放校验     判定日志重放与清单不一致")
		}
	}
	return b.String()
}

func joinPaths(rs []manifest.ReadRecord) string {
	paths := make([]string, len(rs))
	for i, r := range rs {
		paths[i] = r.Path
	}
	const max = 8
	if len(paths) > max {
		return strings.Join(paths[:max], ", ") + fmt.Sprintf(" 等 %d 个", len(paths))
	}
	return strings.Join(paths, ", ")
}
