// Leaf segment of a filesystem path, with home dirs collapsed to ~.
export function pathLeaf(path?: string): string {
  if (!path) return ''
  const trimmed = path.replace(/[\\/]+$/, '')
  if (/^(\/home\/[^/]+|\/Users\/[^/]+|\/root)$/.test(trimmed)) return '~'
  const parts = trimmed.split(/[\\/]/)
  return parts[parts.length - 1] || trimmed
}
