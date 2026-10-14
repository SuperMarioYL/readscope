// Command readscope is the least-privilege workspace facade for Coding
// Agent runs.
//
// One binary, two roles (see the architecture section of the README):
//
//	readscope run      mount the default-deny facade, run the agent
//	                   command inside it, approve denied reads inline,
//	                   and write the per-run exposure manifest
//	readscope inspect  summarize a run's exposure manifest
//
// The same binary also ships a built-in mock agent for demos
// (`readscope mockagent`) so the whole flow is reproducible without
// installing a real Coding Agent.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/SuperMarioYL/readscope/internal/facade"
	"github.com/SuperMarioYL/readscope/internal/grant"
	"github.com/SuperMarioYL/readscope/internal/inspect"
	"github.com/SuperMarioYL/readscope/internal/manifest"
	"github.com/SuperMarioYL/readscope/internal/policy"
	"github.com/SuperMarioYL/readscope/internal/runner"
)

// version is overridden at release time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(2)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "readscope",
		Short: "把工作区以默认拒绝的视图交给 Coding Agent，并留下可审计的暴露清单",
		Long: `ReadScope 把仓库交给 Coding Agent 之前，先交给它一个默认拒绝的工作区视图：

  - 策略允许的源码路径正常读写；
  - 敏感路径（.env、*.pem、.ssh/ ...）一律 Permission denied；
  - 被拒读取在终端内联申请限时授权（只读、TTL 到期自动收回）；
  - 运行结束自动产出 exposure-manifest JSON，readscope inspect 输出摘要。

v0.1 在 Linux 容器内使用（需要 /dev/fuse 与 fusermount）。`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newRunCmd(), newInspectCmd(), newInitCmd(), newVersionCmd())
	mock := newMockAgentCmd()
	mock.Hidden = true
	root.AddCommand(mock)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本号",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("readscope %s\n", version)
		},
	}
}

// ---- run ----

type runFlags struct {
	src          string
	policyPath   string
	mountpoint   string
	outDir       string
	modelTarget  string
	ttl          time.Duration
	noInteractive bool
	docker       bool
}

func newRunCmd() *cobra.Command {
	flags := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run -- <agent 命令>",
		Short: "挂载默认拒绝的工作区视图并运行 agent 命令",
		Long: `把当前工作区挂载为默认拒绝的 FUSE 视图，在视图内运行 agent 命令。

被拒读取会在终端内联请求限时授权；运行结束在输出目录写入
exposure-manifest-<run_id>.json 并打印摘要。`,
		Example: `  readscope run --policy policies/default.yaml -- claude "修复这个 bug"
  readscope run --model deepseek-v4.1-flash -- readscope mockagent`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.docker {
				return fmt.Errorf("run --docker 计划在 v0.1 的 m3 里程碑提供（一条命令端到端容器运行）；" +
					"当前版本请按 README「在容器内运行」一节的手动 docker 命令执行")
			}
			if len(args) == 0 {
				return fmt.Errorf("缺少 agent 命令：请用 `readscope run -- <命令>` 传入")
			}
			return runAgent(flags, args)
		},
	}
	cmd.Flags().StringVar(&flags.src, "src", ".", "工作区源目录（挂载为视图的原始文件）")
	cmd.Flags().StringVar(&flags.policyPath, "policy", "", "策略文件（默认依次尝试 ./policies/default.yaml、./readscope.yaml）")
	cmd.Flags().StringVar(&flags.mountpoint, "mount", "", "视图挂载点（默认临时目录）")
	cmd.Flags().StringVar(&flags.outDir, "out", ".readscope/runs", "运行产物目录（判定日志、授权记录、暴露清单）")
	cmd.Flags().StringVar(&flags.modelTarget, "model", "", "本次运行的模型目标（仅记录在清单中，如 deepseek-v4.1-flash）")
	cmd.Flags().DurationVar(&flags.ttl, "ttl", 30*time.Minute, "内联授权的默认有效期")
	cmd.Flags().BoolVar(&flags.noInteractive, "no-interactive", false, "非交互模式：被拒读取直接拒绝，不询问（CI 用）")
	cmd.Flags().BoolVar(&flags.docker, "docker", false, "在 Docker 容器内运行（m3 里程碑，尚未提供）")
	return cmd
}

func resolvePolicyPath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	for _, candidate := range []string{"policies/default.yaml", "readscope.yaml"} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到策略文件：请通过 --policy 指定（默认策略见仓库 policies/default.yaml）")
}

func newRunID(now time.Time) string {
	var rnd [3]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return now.UTC().Format("20060102-150405") + "-000000"
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(rnd[:])
}

func runAgent(flags *runFlags, agentCmd []string) error {
	startedAt := time.Now()
	runID := newRunID(startedAt)

	policyPath, err := resolvePolicyPath(flags.policyPath)
	if err != nil {
		return err
	}
	pol, err := policy.Load(policyPath)
	if err != nil {
		return err
	}

	src, err := filepath.Abs(flags.src)
	if err != nil {
		return err
	}
	if st, err := os.Stat(src); err != nil {
		return fmt.Errorf("工作区源目录 %s: %w", src, err)
	} else if !st.IsDir() {
		return fmt.Errorf("工作区源目录 %s 不是目录", src)
	}

	mountpoint := flags.mountpoint
	if mountpoint == "" {
		mountpoint = filepath.Join(os.TempDir(), "readscope-mnt-"+runID)
	}
	mountpoint, err = filepath.Abs(mountpoint)
	if err != nil {
		return err
	}
	if mountpoint == src {
		return fmt.Errorf("挂载点不能与工作区源目录相同")
	}
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("创建挂载点 %s: %w", mountpoint, err)
	}

	runDir := filepath.Join(flags.outDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("创建运行目录 %s: %w", runDir, err)
	}
	decisionsLog, err := manifest.OpenLog(filepath.Join(runDir, "decisions.jsonl"))
	if err != nil {
		return err
	}
	defer decisionsLog.Close()

	grantsSink, err := os.OpenFile(filepath.Join(runDir, "grants.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("打开授权记录: %w", err)
	}
	defer grantsSink.Close()
	store := grant.NewStore(grantsSink)

	interactive, ttyIn := interactiveIO(flags.noInteractive)
	broker := runner.NewBroker(runner.BrokerConfig{
		Store:       store,
		TTL:         flags.ttl,
		Approver:    os.Getenv("READSCOPE_APPROVER"),
		Interactive: interactive,
		In:          ttyIn,
		Out:         os.Stderr,
	})

	f, err := facade.New(facade.Options{
		Source: src,
		Policy: pol,
		Grants: store,
		Broker: broker,
		Log:    decisionsLog,
		Now:    time.Now,
	})
	if err != nil {
		return err
	}

	server, err := f.Mount(mountpoint)
	if err != nil {
		return fmt.Errorf("挂载失败（容器内需要 --cap-add SYS_ADMIN --device /dev/fuse，宿主机需要 fusermount）: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[readscope] 运行 %s\n", runID)
	fmt.Fprintf(os.Stderr, "[readscope] 视图       %s（源: %s）\n", mountpoint, src)
	fmt.Fprintf(os.Stderr, "[readscope] 策略       %s（%s）\n", policyPath, pol.Digest())
	fmt.Fprintf(os.Stderr, "[readscope] 产物目录   %s\n", runDir)

	env := os.Environ()
	if flags.modelTarget != "" {
		env = append(env, "READSCOPE_MODEL_TARGET="+flags.modelTarget)
	}
	env = append(env, "READSCOPE_RUN_ID="+runID, "READSCOPE_WORKSPACE="+mountpoint)
	envNames := runner.EnvNames(env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	exitCode, runErr := runner.Run(runner.Options{
		Ctx:    ctx,
		Cmd:    agentCmd,
		Dir:    mountpoint,
		Env:    env,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	endedAt := time.Now()

	// The view must not outlive the run, whatever happened to the agent.
	if unmountErr := server.Unmount(); unmountErr != nil {
		fmt.Fprintf(os.Stderr, "[readscope] 警告: 卸载视图失败（%v）；可用 fusermount -u %s 清理\n", unmountErr, mountpoint)
	}
	if flags.mountpoint == "" {
		_ = os.Remove(mountpoint) // best effort; fails while still mounted
	}

	decisions, err := manifest.ReadDecisions(filepath.Join(runDir, "decisions.jsonl"))
	if err != nil {
		return err
	}
	m := manifest.FromDecisions(manifest.Header{
		RunID:        runID,
		AgentCmd:     agentCmd,
		ModelTarget:  flags.modelTarget,
		PolicyDigest: pol.Digest(),
		EnvVarNames:  envNames,
		StartedAt:    startedAt,
		EndedAt:      endedAt,
	}, decisions)
	m.SetGrants(allGrantRecords(store))

	manifestPath, err := m.Save(runDir)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr)
	replay, verifyErr := inspect.VerifyReplay(&m, manifestPath)
	var replayPtr *inspect.ReplayResult
	if verifyErr == nil {
		replayPtr = &replay
	}
	fmt.Fprint(os.Stderr, inspect.Summarize(&m, manifestPath, replayPtr))
	fmt.Fprintf(os.Stderr, "[readscope] 运行结束，退出码 %d\n", exitCode)

	if runErr != nil {
		return runErr
	}
	if exitCode != 0 {
		return errExitCode(exitCode)
	}
	return nil
}

// allGrantRecords snapshots every grant recorded during the run,
// including ones whose TTL already expired.
func allGrantRecords(store *grant.Store) []manifest.GrantRecord {
	recorded := store.All()
	out := make([]manifest.GrantRecord, 0, len(recorded))
	for _, g := range recorded {
		out = append(out, manifest.GrantRecord{
			Path:      g.Path,
			TTL:       g.TTL,
			GrantedAt: g.GrantedAt,
			ExpiresAt: g.ExpiresAt,
			Approver:  g.Approver,
			Reason:    g.Reason,
		})
	}
	return out
}

// interactiveIO reports whether grant prompts may be shown and where
// answers come from: the controlling terminal when there is one (the
// agent child shares stdin, so the tty is the reliable channel), stdin
// otherwise (pipes; EOF or a non-answer denies).
func interactiveIO(noInteractive bool) (bool, io.Reader) {
	if noInteractive {
		return false, strings.NewReader("")
	}
	if tty, err := os.Open("/dev/tty"); err == nil {
		return true, tty
	}
	return true, os.Stdin
}

type exitCodeError int

func errExitCode(code int) error { return exitCodeError(code) }

func (e exitCodeError) Error() string {
	return fmt.Sprintf("agent 退出码 %d", int(e))
}

// ---- inspect ----

func newInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <清单文件或运行目录>",
		Short: "输出一次运行的暴露清单摘要",
		Args:  cobra.ExactArgs(1),
		Example: `  readscope inspect .readscope/runs/20261015-090000-a1b2c3
  readscope inspect exposure-manifest-20261015-090000-a1b2c3.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, path, err := inspect.Load(args[0])
			if err != nil {
				return err
			}
			replay, verifyErr := inspect.VerifyReplay(m, path)
			var replayPtr *inspect.ReplayResult
			if verifyErr == nil {
				replayPtr = &replay
			}
			fmt.Print(inspect.Summarize(m, path, replayPtr))
			return nil
		},
	}
}

// ---- init: documented m3 stub ----

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "扫描仓库并生成默认策略（m3 里程碑，尚未提供）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New(
				"readscope init 计划在 v0.1 的 m3 里程碑提供（扫描仓库自动生成默认策略）；" +
					"当前版本请复制仓库自带的 policies/default.yaml 并按需调整")
		},
	}
}

// ---- mockagent: built-in demo agent ----

func newMockAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mockagent",
		Short:  "内置演示用 mock agent（模拟一次编码智能体的文件访问序列）",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMockAgent(os.Stdout)
		},
	}
}

func runMockAgent(out io.Writer) error {
	say := func(format string, a ...any) {
		fmt.Fprintf(out, "[agent] "+format+"\n", a...)
	}
	say("编码智能体（演示用 mock）启动，工作目录 %s", mustWd())

	// 1. 探索工作区：默认拒绝视图只显示策略允许的条目。
	entries, err := os.ReadDir(".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	say("浏览工作区: %s", strings.Join(names, "  "))

	// 2. 读源码：策略允许，正常透传。
	if data, err := os.ReadFile("src/main.go"); err == nil {
		say("读取 src/main.go（%d 字节）— 策略允许，透传", len(data))
	} else if data, err := os.ReadFile("main.go"); err == nil {
		say("读取 main.go（%d 字节）— 策略允许，透传", len(data))
	}

	// 3. 尝试读敏感文件：门面拒绝，触发内联授权询问（终端里 y/N）。
	for _, secret := range []string{".env", ".ssh/id_ed25519"} {
		say("请求读取 %s …", secret)
		data, err := os.ReadFile(secret)
		if err != nil {
			if os.IsPermission(err) {
				say("%s → 被门面拒绝（EACCES）", secret)
			} else {
				say("%s → 读取失败: %v", secret, err)
			}
			continue
		}
		// 授权放行：内容不得打印——这里只证明读到了。
		say("%s → 可读（%d 字节），已授权放行，内容进入模型上下文", secret, len(data))
		_ = data
	}

	// 4. 写源码：策略允许的路径可写。
	if err := os.WriteFile("src/readscope-demo.md", []byte("# demo\n\n由 mock agent 在默认拒绝视图内写入。\n"), 0o644); err != nil {
		say("写入 src/readscope-demo.md 失败: %v", err)
	} else {
		say("写入 src/readscope-demo.md — 策略允许，透传")
	}

	// 5. 对照组：原始路径不经门面，仍然可读——证明拒绝发生在门面层，
	//    也说明门面不是沙箱边界（README 中的边界声明）。
	if backing := os.Getenv("READSCOPE_SRC"); backing != "" {
		if data, err := os.ReadFile(filepath.Join(backing, ".env")); err == nil {
			say("对照: 直读原始路径 %s/.env（%d 字节）→ 可读 —— 拒绝确实发生在门面层", backing, len(data))
		} else {
			say("对照: 直读原始路径 %s/.env 失败: %v", backing, err)
		}
	}

	say("完成，正常退出")
	return nil
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "?"
	}
	return wd
}
