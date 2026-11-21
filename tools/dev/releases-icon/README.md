# Releases app icon

Amber disc with a white up arrow. Unravis is a dark rounded square with teal bars, so the two differ in shape and colour at 16, 32 and 256 px.

`python3 make_icon.py` writes `releases.ico` (needs Pillow; run it with `/tmp/unravis-icon.png` absent the preview step fails, ignore it). `python3 make_syso.py` turns the .ico into `cmd/unravis-releases/rsrc_windows_amd64.syso` (the exe icon). Copy the .ico to `cmd/unravis-releases/ui/favicon.ico` (the window and taskbar icon).
