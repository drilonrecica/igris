# VHS tape for the README demo: igris runs the parallel phase D1 of
# docs/demo/tasks.md in herdr with real Claude Code sessions, from home to the
# end-of-run summary. Run it with `make demo` from the repo root (see
# docs/demo/README.md); it writes docs/demo/igris-demo.gif.
#
# Claude's timing varies, so the tape waits on what igris shows instead of
# fixed sleeps, and hides the long stretches where the sessions are just working.

Output docs/demo/igris-demo.gif

Require igris
Require herdr
Require claude

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 16
Set Framerate 15
Set TypingSpeed 50ms
Set Theme { "name": "igris", "background": "#05070F", "foreground": "#EAF4FF", "cursor": "#3CC8FF", "selection": "#123A7A", "black": "#05070F", "red": "#E3172B", "green": "#4ADE80", "yellow": "#FACC15", "blue": "#3CC8FF", "magenta": "#B48CFF", "cyan": "#3CC8FF", "white": "#EAF4FF", "brightBlack": "#5B6B8C", "brightRed": "#FF4D5E", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#B8F0FF", "brightMagenta": "#D8B4FE", "brightCyan": "#B8F0FF", "brightWhite": "#FFFFFF" }

# Set up the scratch project. It is a new folder each time, so Claude Code
# asks once whether you trust it: answer that off camera, in a claude started
# and left without a prompt (nothing is sent). The tasks' worktrees lie inside
# the folder and are trusted with it.
Hide
Type `herdr session stop igris-demo; herdr session delete igris-demo; clear` Enter
Sleep 1s
Type `export PS1='$ ' && cd "$(sh docs/demo/setup.sh)" && clear` Enter
Wait /\$$/
Type "claude" Enter
Wait+Screen@30s /Yes, I trust this folder/
Sleep 2s
Down
Sleep 500ms
Enter
Wait+Screen@30s /shift\+tab|for shortcuts/
Type "/exit"
Sleep 1s
Enter
Wait@30s /\$$/

# Open the project in a herdr session of its own, so the recording never
# touches your everyday herdr session. A session left over from a failed
# recording is removed first (above). ctrl+b b hides herdr's sidebar, once it
# is drawn, so igris gets the full width.
Type "herdr --session igris-demo" Enter
Wait+Screen@30s /spaces/
Sleep 2s
Ctrl+B
Type "b"
Sleep 1s
Type `export PS1='$ ' && clear` Enter
Sleep 1s
Type "igris" Enter
Wait+Screen@30s /Quit/
Sleep 1s
Show

# Home: phase D1 is ready, two tasks on two ranks.
Sleep 4s

# The start wizard: phase D1, mode auto (Claude Code approves safe actions
# itself; no skip-permissions), Arise.
Type "a"
Sleep 2s
Enter
Sleep 2s
Type "3"
Sleep 3s
Enter

# Arise. D1 is a parallel phase: both tasks start at once, each in a fresh
# session on its own rank and in a worktree of its own.
Wait+Screen@1m /Arise\./
Sleep 12s

# Each task that passes verify asks whether to commit (commit = "ask").
Hide
Wait+Screen@10m /Commit D1-0[12]\?/
Show
Sleep 4s
Type "1"
Sleep 4s

Hide
Wait+Screen@10m /Commit D1-0[12]\?/
Show
Sleep 2s
Type "1"

# The phase completes: the end-of-run summary.
Wait+Screen@5m /Copy report command/
Sleep 8s

# Quit igris, leave and stop the demo herdr session.
Hide
Type "q"
Sleep 1s
Type "q"
Sleep 1s
Ctrl+B
Type "q"
Sleep 1s
Type "herdr session stop igris-demo && herdr session delete igris-demo" Enter
Sleep 1s
