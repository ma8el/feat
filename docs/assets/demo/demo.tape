# Records docs/assets/demo.gif. From the repository root:
#   make build && vhs docs/assets/demo/demo.tape
# The agents are real Claude Code sessions, so every take differs.

Output docs/assets/demo.gif

Set Shell zsh
Set FontSize 14
Set Width 1400
Set Height 800
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Hide
Type "source docs/assets/demo/setup.sh" Enter
Wait@90s
Show

Type `feat implement --project shop --brief "Paginate the product list"`
Sleep 500ms Enter
Sleep 2s
Type `feat implement --project shop --brief "Add a /health endpoint that returns ok"`
Sleep 500ms Enter
Sleep 2s

Hide
Type "clear" Enter
Show

# The dashboard opens on the newest task, the health endpoint.
Type "feat" Sleep 300ms Enter
Sleep 7s

# Cut the agent's remaining work out of the recording.
Hide
Sleep 35s
Show

Type "L" Sleep 1s
Type "L" Sleep 1s
Type "L" Sleep 2s
Type "c" Sleep 3s
Type "u" Sleep 5s
Type "q" Sleep 1s
Hide
Type "clear" Enter
Show

Type "curl localhost:21000/health" Sleep 500ms Enter
Sleep 4s
