**English** | [简体中文](./README.md)

<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/hero-dark.svg">
  <img src="assets/hero-light.svg" alt="ReadScope — a default-deny workspace view for Coding Agents, with an auditable exposure manifest" width="100%">
</picture>

[![License](https://img.shields.io/badge/license-MIT-green)](./LICENSE)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![CI](https://img.shields.io/github/actions/workflow/status/SuperMarioYL/readscope/ci.yml?branch=main&label=ci)](https://github.com/SuperMarioYL/readscope/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/SuperMarioYL/readscope)](https://github.com/SuperMarioYL/readscope/releases)

**Before you hand a repo to a Coding Agent, hand it a default-deny view of the workspace: sensitive files stay invisible, out-of-scope reads need just-in-time approval, and every Agent run leaves behind an auditable exposure manifest.**

ReadScope is a least-privilege workspace facade for platform and security engineers: inside a container, the repository is projected into a policy-trimmed view where `cat .env` gets `Permission denied` on the spot, denied reads can be approved inline in the terminal as read-only, time-limited grants, and every run ends with an `exposure-manifest` JSON — machine-checkable, replay-verifiable, and a natural audit artifact for data-security reviews.

</div>

<br/>

## Contents

- [Why](#why)
- [Real demo](#real-demo)
- [How it works](#how-it-works)
- [Install](#install)
- [Up and running in ten minutes](#up-and-running-in-ten-minutes)
- [Usage](#usage)
- [Policy files](#policy-files)
- [The exposure manifest](#the-exposure-manifest)
- [Who covers what](#who-covers-what)
- [Security boundary and limitations](#security-boundary-and-limitations)
- [Roadmap](#roadmap)
- [Pricing](#pricing)
- [Contributing](#contributing)

## Why

Handing a whole repository to an Agent run hands over `.env`, private keys and customer data with it. The common illusion is "the sandbox already blocks egress, so we're fine" — **egress controls are not read controls**: every file the model can read inside the sandbox has already left your boundary.

The tooling around this problem each owns one slice: env-file tools manage `.env` values, credential proxies keep real secrets out of the Agent's hands, sandbox vendors own execution and networking. But **which files this particular run can read** has no owner. That is the layer ReadScope adds:

- **Default deny**: policy-allowed source paths read and write normally; every other path gets `EACCES` on `open`/`stat`, and denied entries do not even appear in directory listings;
- **Just-in-time grants**: when the Agent hits a denied path, an inline y/N appears in the terminal — approval opens the path read-only for this run with an automatic TTL expiry; refusal is immediate, and writes to sensitive paths are never grantable;
- **Per-run record**: every run writes an exposure manifest — per-path read verdicts, every grant, the names of injected environment variables, the policy digest — derived by replaying the decision log, so the same log always replays into the same manifest.

## Real demo

The recording below is a real end-to-end run (not an animated mock-up): the default-deny view is mounted in a container and the built-in mock agent reads source (passes through), asks for `.env` (denied, then approved inline), is refused on `.ssh/id_ed25519`, writes a source file, and the manifest lands — re-verified on the host. The recording script lives at [docs/demo.tape](docs/demo.tape); the step-by-step facts are recorded in [docs/demo-results.json](docs/demo-results.json).

<p align="center">
  <img src="docs/assets/demo-exposure-manifest.gif" alt="Real ReadScope run: the agent is denied on .env, gets an inline grant, is refused on .ssh, and the exposure manifest lands" width="100%">
</p>

One of the artifacts the run leaves behind is the manifest itself (excerpted from the recording above; full sample in [docs/exposure-manifest.sample.json](docs/exposure-manifest.sample.json)):

```json
{
  "schema": "readscope.exposure-manifest/v0",
  "run_id": "20261014-235739-63de5f",
  "agent_cmd": ["readscope", "mockagent"],
  "model_target": "deepseek-v4.1-flash",
  "policy_digest": "sha256:2c261f538cc3cdbcbc6386097290d349582f587c75a1bda77dac00d39452302b",
  "reads": [
    { "path": "src/main.go", "verdict": "allowed" },
    { "path": ".env", "verdict": "granted" },
    { "path": ".ssh/id_ed25519", "verdict": "denied" }
  ],
  "grants": [
    { "path": ".env", "ttl": "30m0s", "approver": "terminal", "reason": "approved in terminal" }
  ]
}
```

`readscope inspect` answers "what could this run read?" in one screen (the summary is rendered in Chinese; names only, never values):

```text
── 暴露清单摘要 ──────────────────────────────
运行 ID      20261014-235739-63de5f
Agent 命令   readscope mockagent
模型目标     deepseek-v4.1-flash
策略摘要     sha256:2c261f538cc3cdbcbc6386097290d349582f587c75a1bda77dac00d39452302b
起止时间     2026-10-14 23:57:39 → 23:57:42（历时 3s）
读取判定     共 3 个路径：允许 1 · 授权 1 · 拒绝 1
  允许读取   src/main.go
  授权读取   .env
  拒绝读取   .ssh/id_ed25519
限时授权     1 条
  .env  TTL 30m0s  审批人 terminal  授予 23:57:40
环境变量     10 个名字（仅记录名字，不记录值）: HOME, HOSTNAME, PATH, …
重放校验     判定日志重放与清单一致
```

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/architecture-dark.svg">
  <img src="assets/architecture-light.svg" alt="ReadScope architecture: the host readscope CLI starts a container; inside, the policy loader, FUSE facade, grant store, runner and manifest writer each play their part" width="100%">
</picture>

One binary, two roles, zero services, zero databases:

- **On the host**, `readscope run` orchestrates (a one-command `--docker` wrapper is planned for m3);
- **Inside the container**, the same readscope binary loads the policy, mounts the FUSE facade, starts the Agent with the view as its working directory, appends every decision to `decisions.jsonl` in real time, and at run end replays that log into `exposure-manifest-<run_id>.json`.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/process-dark.svg">
  <img src="assets/process-light.svg" alt="One ReadScope run: load the policy, mount the view, run the agent, route denied reads through inline approval, and write the manifest at the end" width="100%">
</picture>

The key design decision: **intercept at the filesystem layer with FUSE instead of instrumenting the Agent**. `cat`, `grep`, `find`, tools written in any language — everything is covered with zero cooperation; no SDK integration required. The manifest is derived by replaying the decision log and verified on the spot — edit the manifest and the replay stops matching, so audits never have to trust the runtime's memory.

## Install

v0.1 targets **Linux containers** (needs `/dev/fuse` and `fusermount`); macOS and Windows users run it through Docker (see the ten-minute path below).

```bash
# Option 1: grab the single binary from GitHub Releases (goreleaser artifacts, checksums.txt included)
#   https://github.com/SuperMarioYL/readscope/releases

# Option 2: go install (requires Go 1.24+)
go install github.com/SuperMarioYL/readscope/cmd/readscope@latest

# Option 3: build from source
git clone https://github.com/SuperMarioYL/readscope.git
cd readscope && go build -o readscope ./cmd/readscope
```

## Up and running in ten minutes

From clone to the first look at "everything this run could have read":

```bash
# 1. Build (the host binary runs inspect; the linux binary runs the facade in the container)
git clone https://github.com/SuperMarioYL/readscope.git && cd readscope
go build -o readscope ./cmd/readscope

# 2. One script, end to end: cross-compiles the linux binary, mounts the default-deny
#    view inside a container and runs the built-in mock agent (answer y at the .env
#    prompt, n at the .ssh prompt)
./examples/run-demo-docker.sh

# 3. Re-verify the summary on the host (manifest and decision log stay in /tmp/readscope-demo-out)
go run ./cmd/readscope inspect /tmp/readscope-demo-out
```

`examples/run-demo-docker.sh` uses the fixture workspace shipped in the repo, [examples/demo-workspace](examples/demo-workspace) (a fake project with placeholder secrets), copied to a temp dir at runtime — nothing is written into your checkout. To run it against a real repository and a real Agent command:

```bash
# Pick GOARCH to match the host Docker architecture (arm64 on Apple Silicon, amd64 on most CI)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/readscope-linux ./cmd/readscope

docker run --rm -it --cap-add SYS_ADMIN --device /dev/fuse \
  -v /tmp/readscope-linux:/usr/local/bin/readscope:ro \
  -v "$PWD":/src \
  -v "$PWD/policies":/policies:ro \
  -v /tmp/readscope-runs:/out \
  alpine:3.20 sh -c 'apk add --no-cache fuse >/dev/null && \
    readscope run --src /src --policy /policies/default.yaml --out /out \
      --model deepseek-v4.1-flash -- claude "fix this bug"'

# When the run ends: the manifest is in /tmp/readscope-runs/<run_id>/, verify from the host
go run ./cmd/readscope inspect /tmp/readscope-runs
```

> `readscope run --docker` (wrapping the docker invocation above into one command) and `readscope init` (scanning the repo to generate a default policy) are planned for the m3 milestone; the current version returns an explanatory error. Until then, use the manual container command above together with the shipped [policies/default.yaml](policies/default.yaml).

## Usage

```text
readscope run -- <agent command>       Mount the default-deny view and run the agent inside it
  --src <dir>              Workspace source directory (default .)
  --policy <file>          Policy file (tries ./policies/default.yaml, then ./readscope.yaml)
  --mount <dir>            Mountpoint for the view (default: temp dir)
  --out <dir>              Run artifact directory (default .readscope/runs)
  --model <label>          Model target label, recorded in the manifest (e.g. deepseek-v4.1-flash)
  --ttl <duration>         Default lifetime for inline grants (default 30m)
  --no-interactive         Deny refused reads without asking (for CI)
  --docker                 Container orchestration (m3 milestone, not yet available)

readscope inspect <file|dir>           Summarize an exposure manifest (a directory picks the newest run)
readscope init                     Generate a default policy by scanning the repo (m3, not yet available)
readscope version                  Print the version
```

Environment variables: `READSCOPE_APPROVER` overrides the recorded approver name (default `terminal`); `READSCOPE_DEBUG` enables FUSE request logging. The injected `READSCOPE_RUN_ID`, `READSCOPE_WORKSPACE` and `READSCOPE_MODEL_TARGET` names are recorded in the manifest.

## Policy files

```yaml
default: deny            # v0.1 supports default-deny only
rules:
  # Rules match in order; the first hit wins; empty verbs = explicit deny
  - glob: ".env*"
    explain: "dotenv files and secrets"
  - glob: "src/**"
    verbs: [read, write]
  - glob: "*.md"
    verbs: [read]
```

Semantics:

- Globs containing `/` match workspace-relative paths, where `**` crosses directories and `*` does not; a pattern ending in `/**` also matches the directory itself (so the directory stays listable);
- Globs without `/` match the base name at any depth (`*.pem` also covers `certs/server.pem`);
- Any path no rule covers is denied; denied paths are excluded from listings and their metadata is replaced with a type-only placeholder (`stat` leaks nothing either).

The shipped [policies/default.yaml](policies/default.yaml) covers the common sensitive paths (`.env*`, `*.pem`, `*.key`, `.aws/`, `.ssh/`, `.git-credentials`, `.git/config`) and common source paths — use it as-is or adapt it.

## The exposure manifest

Every run writes:

```text
.readscope/runs/<run_id>/
├── decisions.jsonl                    # Decision log: every open/stat/list/write appended in real time
├── grants.jsonl                       # Grant record: path, TTL, approver, reason
└── exposure-manifest-<run_id>.json    # The exposure manifest, replayed from the decision log
```

The format is specified in [schemas/exposure-manifest.v0.json](schemas/exposure-manifest.v0.json) (JSON Schema, validatable with any validator). `reads` holds the final verdict per read path (`allowed` / `granted` / `denied`), `grants` holds every time-limited grant (expired ones included), and `env_var_names` records only the names of the variables the Agent received — never values. `readscope inspect` replays `decisions.jsonl` on the spot to verify the manifest; any mismatch is flagged explicitly.

## Who covers what

The tooling around "agents and credentials" each owns one slice. This table explains the layers and where ReadScope sits (based on the public documentation of each project as of 2026-10; it is not a full evaluation of any product):

| Layer | Representative tools | Coverage |
| --- | --- | --- |
| env-file values | varlock and similar | env-file schema and runtime injection |
| Credential proxy | Infisical agent-vault and similar | Agents never hold real credentials |
| Sandbox and egress | E2B / Daytona / OpenSandbox | Execution and network boundaries |
| **Workspace read projection + JIT file grants + per-run exposure manifest** | **ReadScope** | **File-level visibility of this run** |

These layers are complementary: credential proxies own the values, ReadScope owns "which files this run could read", and turns the answer into an auditable record.

## Security boundary and limitations

Stated plainly:

- **The facade is not a sandbox boundary**. The FUSE view constrains access that goes through the view; a privileged process inside the container (root, for instance) can reach the backing path directly and bypass the view. The demo recording deliberately keeps the control read of the raw path to show exactly this. ReadScope raises the floor and leaves evidence; it complements a sandbox's execution/network boundary.
- **Environment variables are recorded by name only; values are not hidden**. Value masking and credential proxying belong to other tools; v0.1 does not do this.
- **Single machine, single run**. The manifest is a per-run local artifact; team-level retention and aggregation come with the v0.2 hosted service.
- **m1+m2 is the current completeness**. `readscope init` and `readscope run --docker` are not implemented yet (see the roadmap); the manual container command works today.
- **Performance**. The facade adds one policy decision per path operation — negligible for CLI workloads. No benchmarks have been run and no performance claims are made.

## Roadmap

- [x] **m1** Default-deny view inside a container: policy enforced, sensitive paths get `EACCES` on open, the raw path stays readable (the control)
- [x] **m2** Inline time-limited grants + exposure manifest: terminal y/N, automatic TTL expiry, manifest on disk, `inspect` summary, decision-log replay verification
- [ ] **m3** `readscope init` generates a default policy in one step; `readscope run --docker` as a one-command path; a Gitee mirror
- [ ] **v0.2** Hosted policy and grant-approval server (see [Pricing](#pricing)): team-wide policy management, approvals beyond the terminal, manifest retention and aggregation

## Pricing

ReadScope is open core plus a hosted service; the paid path is a committed, public roadmap item:

- **Open core (this repo)**: the CLI, the policy format and the exposure-manifest format, free to use in commercial settings.
- **Hosted policy and grant-approval server (from v0.2)**: for teams running Agents across many workspaces — centralized policy management, approvals beyond the terminal (a security lead can approve), manifest retention and monthly aggregated audit reports. Priced per **workspace-owner seat**: **¥199/seat/month (about $29), 5-seat minimum**. The first three design partners join at a flat **¥2,000/month**, invoiced; self-serve (international cards / WeChat Pay) follows.
- The manifest format stays open: the hosted service produces exactly the same `exposure-manifest` as self-hosting, so your audit trail is portable by construction.

## Contributing

Issues and PRs are welcome. For bug reports the minimal set is: the policy file, the run command and the exposure manifest (redacted).

<p align="center"><sub><a href="./LICENSE">MIT</a> © 2026 SuperMarioYL</sub></p>
