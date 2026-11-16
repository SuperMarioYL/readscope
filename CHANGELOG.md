# Changelog

All notable changes to ReadScope are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions match the
`vX.Y.Z` release tags.

## v0.2.0 — 2026-11-17

Audit-truthfulness release: every change closes a way for the exposure manifest
to disagree with what the run could actually read. No new commands or policy
syntax.

### Fixed

- **Hardlinks can no longer launder a denied file.** A hardlink is admitted only
  when the destination is writable *and* the source is readable in its own right
  (policy allow or an active grant). Previously `ln .env src/leak` inside the view
  published the secret under an allowed name, and the manifest recorded
  `src/leak` as an allowed read.
- **Replay verification now covers the grants.** `readscope inspect` replays
  `grants.jsonl` and compares it with the manifest's `grants` array. Previously
  the grants were copied out of the manifest under test, so editing them could
  not be detected.
- **A corrupt log is reported instead of silently skipped.** `decisions.jsonl` or
  `grants.jsonl` that exists but cannot be parsed now produces an explicit
  "cannot verify" line, and `readscope inspect` exits non-zero. An absent log
  still means "nothing to verify".
- **`**/` in a glob requires a directory boundary.** `**/secret.txt` no longer
  matches `mysecret.txt`; allow rules no longer widen silently.
- **`readscope version` reports the real version for `go install`.** The
  documented `go install ...@latest` path cannot set linker flags, so the binary
  used to print `dev`; it now falls back to the module version. Local `go build`
  checkouts still print `dev`.

### Changed

- README (zh/en) and the product site describe the new verification behavior and
  the symlink limitation in the security section.
- The roadmap's hosted policy/approval server moved from "v0.2" to "v0.3"; this
  repository ships a CLI, and v0.2.0 does not include a hosted service.

## v0.1.0 — 2026-10-15

First release.

- Default-deny FUSE workspace view driven by a YAML policy (`default: deny`,
  first-match-wins glob rules, empty verbs for explicit denies).
- Inline y/N approval for denied reads, recorded as read-only grants with a TTL.
- Per-run `exposure-manifest-<run_id>.json` derived by replaying
  `decisions.jsonl`, plus `readscope inspect` for a one-screen summary.
- `readscope run` / `inspect` / `version`; built-in `mockagent` for the demo.
- Linux container runtime (`/dev/fuse` + `fusermount`); goreleaser binaries for
  linux/darwin/windows.
