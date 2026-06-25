// Shared session action calls used by both the Sidebar context menu and the
// Overview SessionActionsMenu. Pure API wrappers: callers own their own UI
// state (rename input, naming spinner, kill confirms, optimistic removal).

const JSON_HEADERS = { 'Content-Type': 'application/json' }

// Sets the friendly display label only; the underlying tmux session name is
// left untouched (renaming it would break session keys, attachment, and agent
// hooks). clear=false marks it user-set so the AI namer never overwrites it.
// The new label arrives via the websocket state update.
export async function renameSession(name: string, displayName: string, host?: string): Promise<void> {
  try {
    await fetch('/api/session/display-name', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ session: name, display_name: displayName, clear: false, host: host || undefined }),
    })
  } catch (err) {
    console.error('Failed to rename session:', err)
  }
}

// (Re)generates an AI name. The new name arrives via the websocket state
// update; failures surface as backend notice toasts.
export async function aiNameSession(name: string, host?: string): Promise<void> {
  try {
    const res = await fetch('/api/session/regenerate-name', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ session: name, host: host || undefined }),
    })
    if (!res.ok && res.status !== 204) {
      console.error('AI name failed:', res.status, await res.text().catch(() => ''))
    }
  } catch (err) {
    console.error('Failed to AI name session:', err)
  }
}

export async function killSession(id: string, name: string, host?: string, removeWorktree?: boolean): Promise<void> {
  try {
    await fetch('/api/session/kill', {
      method: 'POST',
      headers: JSON_HEADERS,
      body: JSON.stringify({ id, name, host: host || undefined, remove_worktree: removeWorktree || undefined }),
    })
  } catch (err) {
    console.error('Failed to kill session:', err)
  }
}
