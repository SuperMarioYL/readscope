# ReadScope 10-minute happy path, recorded end to end.
# Output: docs/assets/demo-exposure-manifest.gif (README demo section).
#
#   vhs docs/demo.tape        # from the repository root
#
# The tape drives the real binary: examples/run-demo-docker.sh
# cross-compiles the linux build, mounts the default-deny view inside a
# container, runs the built-in mock agent, and lands the exposure
# manifest. Two inline approvals are answered live: y for .env, n for
# .ssh/id_ed25519. The WaitPattern anchors to the bare shell prompt or
# the readscope "(y/N) " prompt so each Wait resumes exactly when the
# run asks or finishes (a bare token would also match the echoed
# command line, so the pattern must be end-of-line anchored; vhs only
# honours Set at the top of a tape, hence one combined pattern).
Output docs/assets/demo-exposure-manifest.gif

Set Shell bash
Set FontSize 15
Set Width 1200
Set Height 740
Set Margin 20
Set Framerate 24
Set WaitTimeout 10s
Set TypingSpeed 30ms
Set WaitPattern "»$|\(y/N\)$"
Set Theme { "background": "#060c0d", "foreground": "#f8fdee", "cursor": "#7de656", "cursorText": "#060c0d", "selectionBackground": "#203c1d", "colors": ["#0e1717", "#e06c75", "#7de656", "#dbf6ac", "#6ae78b", "#c586c0", "#9cdcfe", "#f8fdee", "#5d7a6b", "#e06c75", "#a5e887", "#e8f8c8", "#8ee8a8", "#d9a8e8", "#b8dcf0", "#ffffff"] }

# Warm caches off the record; pin a stable shell prompt for Wait.
Hide
Type "export PS1='» '; clear" Enter
Sleep 400ms
Type "go build ./... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/readscope && docker pull alpine:3.20 >/dev/null 2>&1 && echo warm" Enter
Wait@90s
Show

Type "clear" Enter
Sleep 800ms
Type "./examples/run-demo-docker.sh" Enter
Wait@150s
Sleep 1200ms
Type "y" Enter
Wait@90s
Sleep 1200ms
Type "n" Enter
Wait@150s
Sleep 1000ms
Type "go run ./cmd/readscope inspect /tmp/readscope-demo-out" Enter
Wait@90s
Sleep 4000ms
