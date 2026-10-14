// Package runner runs the agent command inside the facade view and
// handles the inline grant approval flow.
//
// The broker is the only place a denied read can turn into a grant:
// it prints the request to the run's terminal, waits for a single
// y/N answer, and on approval records a time-limited, read-only grant.
// One path is asked at most once per run, and prompts are serialized so
// concurrent FUSE requests cannot stack up terminals of questions.
package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SuperMarioYL/readscope/internal/grant"
)

// BrokerConfig configures the inline approval flow.
type BrokerConfig struct {
	// Store receives approved grants.
	Store *grant.Store
	// TTL is the lifetime of approved grants.
	TTL time.Duration
	// Approver is recorded on each grant ("terminal" by default, or
	// the value of READSCOPE_APPROVER).
	Approver string
	// Interactive enables the y/N prompt. Without it every denied
	// read is refused (CI mode).
	Interactive bool
	// In is where the y/N answer is read from (the terminal, or stdin).
	In io.Reader
	// Out is where prompts are written (the terminal, or stderr).
	Out io.Writer
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Broker implements facade.Broker with the inline terminal flow.
type Broker struct {
	cfg    BrokerConfig
	mu     sync.Mutex
	asked  map[string]bool
	reader *bufio.Reader
}

// NewBroker builds the broker for one run.
func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Approver == "" {
		cfg.Approver = "terminal"
	}
	return &Broker{
		cfg:    cfg,
		asked:  map[string]bool{},
		reader: bufio.NewReader(cfg.In),
	}
}

// RequestGrant asks the operator to approve read access to rel. It
// returns true when an active grant now covers rel.
func (b *Broker) RequestGrant(rel string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.asked[rel] {
		return false
	}
	b.asked[rel] = true

	if !b.cfg.Interactive {
		fmt.Fprintf(b.cfg.Out, "[readscope] 拒绝读取 %s（非交互模式，未询问授权）\n", rel)
		return false
	}

	fmt.Fprintf(b.cfg.Out, "\n[readscope] Agent 请求读取被拒路径: %s\n", rel)
	fmt.Fprintf(b.cfg.Out, "[readscope] 批准本次运行内只读访问（TTL %s）?(y/N) ", formatTTL(b.cfg.TTL))

	line, err := b.reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(b.cfg.Out, "\n[readscope] 未收到答复，拒绝")
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		fmt.Fprintf(b.cfg.Out, "[readscope] 已拒绝 %s\n", rel)
		return false
	}

	now := b.cfg.Now()
	g := grant.Grant{
		Path:      rel,
		TTL:       b.cfg.TTL.String(),
		GrantedAt: now,
		ExpiresAt: now.Add(b.cfg.TTL),
		Approver:  b.cfg.Approver,
		Reason:    "approved in terminal",
	}
	if err := b.cfg.Store.Add(g); err != nil {
		fmt.Fprintf(b.cfg.Out, "[readscope] 授权记录失败，拒绝 %s: %v\n", rel, err)
		return false
	}
	fmt.Fprintf(b.cfg.Out, "[readscope] 已授权 %s（只读，TTL %s，审批人 %s）\n", rel, formatTTL(b.cfg.TTL), b.cfg.Approver)
	return true
}

func formatTTL(d time.Duration) string {
	return d.String()
}

// Options describes one agent run.
type Options struct {
	// Ctx, when canceled, interrupts the agent (SIGINT) and force-kills
	// it after a short grace period.
	Ctx context.Context
	// Cmd is the agent command line, resolved from PATH.
	Cmd []string
	// Dir is the working directory (the facade mountpoint).
	Dir string
	// Env is the child's full environment.
	Env []string
	// Stdio are the child's streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes the agent command synchronously and returns its exit
// code. A signal-interrupted run returns -1.
func Run(opts Options) (int, error) {
	if len(opts.Cmd) == 0 {
		return 0, fmt.Errorf("agent command is empty")
	}
	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, opts.Cmd[0], opts.Cmd[1:]...)
	// Interrupt first so agents can flush state; force-kill if they stall.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", opts.Cmd[0], err)
	}
	err := cmd.Wait()
	var exitErr *exec.ExitError
	if err == nil {
		return 0, nil
	}
	if errors.As(err, &exitErr) {
		// -1 means the process was signaled (e.g. our SIGINT forward).
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("run %s: %w", opts.Cmd[0], err)
}

// EnvNames returns the variable names carried by an environment block,
// deduplicated and sorted.
func EnvNames(env []string) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			name := kv[:i]
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}
