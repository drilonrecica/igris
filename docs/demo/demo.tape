# VHS tape for the README demo: igris runs phase D1 of docs/demo/tasks.md in
# herdr with real Claude Code sessions. Run it with `make demo` from the repo
# root (see docs/demo/README.md); it writes docs/demo/igris-demo.gif.
#
# Claude's timing varies, so the tape waits on igris's own log lines instead
# of fixed sleeps, and hides the long stretches where a session is just working.

Output docs/demo/igris-demo.gif

Require igris
Require herdr

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 16
Set Framerate 15
Set TypingSpeed 50ms
Set Theme { "name": "igris", "background": "#0B1E46", "foreground": "#EAF4FF", "cursor": "#3CC8FF", "selection": "#123A7A", "black": "#05070F", "red": "#E3172B", "green": "#4ADE80", "yellow": "#FACC15", "blue": "#3CC8FF", "magenta": "#B48CFF", "cyan": "#3CC8FF", "white": "#EAF4FF", "brightBlack": "#5B6B8C", "brightRed": "#FF4D5E", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#B8F0FF", "brightMagenta": "#D8B4FE", "brightCyan": "#B8F0FF", "brightWhite": "#FFFFFF" }

# Set up the scratch project and open it in a herdr session of its own, so the
# recording never touches your everyday herdr session. A session left over
# from a failed recording is removed first: its pane still sits in the old,
# deleted scratch dir. ctrl+b b hides herdr's sidebar so igris gets the full
# width.
Hide
Type `herdr session stop igris-demo; herdr session delete igris-demo; clear` Enter
Sleep 1s
Type `export PS1='$ ' && cd "$(sh docs/demo/setup.sh)" && clear` Enter
Wait /\$$/
Type "herdr --session igris-demo" Enter
Sleep 3s
Ctrl+B
Type "b"
Sleep 500ms
Type `export PS1='$ ' && clear` Enter
Sleep 1s
Show

Type "igris status" Sleep 300ms Enter
Sleep 3s
Type "igris arise D1 --mode auto" Sleep 300ms Enter

# D1-01 on sonnet: Claude Code asks to trust the new folder, igris shows the
# task as NEEDS YOU. Jump to the session (o), trust the folder, watch Claude
# start on the task, then go back to igris's tab (herdr: ctrl+b 1).
Wait+Screen@2m /NEEDS YOU/
Sleep 4s
Type "o"
Wait+Screen@30s /Yes, I trust this folder/
Sleep 2s
Down
Sleep 500ms
Enter
Sleep 15s
Ctrl+B
Type "1"
Sleep 3s

Hide
Wait+Screen@10m /D1-01 done/
Show
Sleep 3s

# D1-02 on opus: a fresh session with the next task's model.
Wait+Screen@2m /D1-02 session open/
Sleep 6s

Hide
Wait+Screen@10m /phase D1 complete/
Show
Sleep 6s

# Quit igris, leave and stop the demo herdr session.
Hide
Type "q"
Sleep 1s
Ctrl+B
Type "q"
Sleep 1s
Type "herdr session stop igris-demo && herdr session delete igris-demo" Enter
Sleep 1s
