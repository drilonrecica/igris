# VHS tape for the README's home screenshot (wide): bare `igris` on a copy of
# the example plan with two tasks done. Run it with `make demo-screens` from
# the repo root (see docs/demo/README.md); it writes docs/demo/igris-home.png.
# No Claude Code session starts.

Require igris
Require tmux

Set Shell "bash"
Set FontSize 16
Set Width 1340
Set Height 340
Set Padding 16
Set Theme { "name": "igris", "background": "#05070F", "foreground": "#EAF4FF", "cursor": "#3CC8FF", "selection": "#123A7A", "black": "#05070F", "red": "#E3172B", "green": "#4ADE80", "yellow": "#FACC15", "blue": "#3CC8FF", "magenta": "#B48CFF", "cyan": "#3CC8FF", "white": "#EAF4FF", "brightBlack": "#5B6B8C", "brightRed": "#FF4D5E", "brightGreen": "#86EFAC", "brightYellow": "#FDE68A", "brightBlue": "#B8F0FF", "brightMagenta": "#D8B4FE", "brightCyan": "#B8F0FF", "brightWhite": "#FFFFFF" }

# igris runs in a private tmux server without a status line, so its header
# shows a reachable backend, as in everyday use.
Hide
Type `export PS1='$ ' && cd "$(sh docs/demo/setup.sh home)" && clear` Enter
Wait /\$$/
Type `env -u TMUX tmux -L igris-demo -f /dev/null new-session igris \; set status off` Enter
Wait+Screen /Quit/
Sleep 2s
Show
Screenshot docs/demo/igris-home.png
Sleep 500ms

Hide
Type "q"
Sleep 500ms
Type "exit" Enter
Sleep 500ms
