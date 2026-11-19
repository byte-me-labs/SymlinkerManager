package main

import _ "embed"

// appIconPNG is the window and taskbar icon. The same artwork is compiled into
// the executable's resources through rsrc_windows_amd64.syso (built from
// assets/app.ico), which covers Explorer, shortcuts and the taskbar; this PNG
// also gives the window an icon when no resource object is present, such as
// during `go run`.
//
//go:embed assets/icon.png
var appIconPNG []byte
