package setup

import "api-audit-explorer/internal/shelllink"

// The shortcut writer and reader live in internal/shelllink, so the app and the installer create shortcuts with the same code.
type Shortcut = shelllink.Shortcut

var (
	ReadShortcutTarget = shelllink.ReadShortcutTarget
	ReadShortcutArgs   = shelllink.ReadShortcutArgs
)
