# Keyboard Shortcuts

## Status

Removed all conflicting global keyboard shortcuts (2025-05-10).

## What was removed

- Quick Switcher (`Ctrl/Cmd+K`, `Ctrl/Cmd+P`, `Ctrl+Space`)
- Overview (`Ctrl/Cmd+H`)
- Lock / Sign out (`Ctrl/Cmd+L`)
- Jump to next alert (`Ctrl/Cmd+J`)
- Close active pane (`Ctrl/Cmd+Shift+W`)
- Previous / next pane (`Ctrl/Cmd+Shift+[`, `]`, `Ctrl/Cmd+Alt+Left/Right`)

## What remains

- Help (`Ctrl/Cmd+/` or `?`)
- Toggle sidebar (`Ctrl/Cmd+\`)
- Settings (`Ctrl/Cmd+,`)
- Split pane (`Ctrl/Cmd+Shift+\`)
- Terminal fullscreen (`Ctrl/Cmd+Shift+F`)
- Exit fullscreen (`Esc`)

## TODO

- [ ] Redesign keyboard shortcuts from scratch with conflict analysis against browser and terminal conventions
- [ ] Gate global shortcuts when terminal textarea is focused
- [ ] Add a dedicated keyboard shortcuts settings panel
- [ ] Consider using `Alt` or `Ctrl+Shift` prefixes to avoid browser conflicts
- [ ] Add UI buttons as alternatives to keyboard shortcuts for accessibility
