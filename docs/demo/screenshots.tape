# Renders the README screenshots into docs/images. Run through
# `make screenshots`, which stages docs/demo/compose.yaml and sets
# DOCKMASTER_DEMO_HOST and DOCKMASTER_CONFIG_DIR.
Output "/tmp/dockmaster-demo/tour.gif"

Set Shell "bash"
Set FontSize 14
Set Width 1640
Set Height 900
Set Padding 16
Set TypingSpeed 0
Set Theme "Builtin Dark"

Hide
Type `clear && ./dist/dockmaster --host "$DOCKMASTER_DEMO_HOST" --splashless`
Enter
Sleep 7s
Show

# The containers list, CPU/MEM sampled.
Sleep 3s
Screenshot docs/images/containers.png

# Two rows marked for a bulk action, then unmarked again.
Down
Space
Down
Space
Sleep 1s
Screenshot docs/images/marks.png
Space
Up
Space
Up
Sleep 500ms

# Help.
Type "?"
Sleep 1s
Screenshot docs/images/help.png
Escape
Sleep 500ms

# web's healthcheck.
Type "/web"
Enter
Sleep 500ms
Type "H"
Sleep 2s
Screenshot docs/images/health.png
# Paused apart: an ESC followed at once by a key reads as Alt+key.
Escape
Sleep 500ms
Escape
Sleep 500ms

# The worker's log, with the indicator line.
Type "/worker"
Enter
Sleep 500ms
Enter
Sleep 4s
Screenshot docs/images/logs.png
# Paused apart: an ESC followed at once by a key reads as Alt+key.
Escape
Sleep 500ms
Escape
Sleep 500ms

# Run a container from an image.
Type "1"
Sleep 3s
Type "/nginx"
Enter
Sleep 500ms
Type "u"
Sleep 1s
Type "web-2"
Tab
Type "8090:80"
Sleep 500ms
Screenshot docs/images/run.png
Escape
Sleep 500ms

# A volume's files: db's data directory.
Type "2"
Sleep 3s
Type "/pgdata"
Enter
Sleep 500ms
Enter
Sleep 3s
Screenshot docs/images/volume.png

Hide
Type ":q"
Enter
