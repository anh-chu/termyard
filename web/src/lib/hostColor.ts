// Deterministic per-host accent color. Same fingerprint always lands on the
// same palette slot across renders and across machines (assuming both sides
// share this palette). The local host gets `null` so callers can omit the
// stripe entirely (no visual noise for "this is me").
//
// Palette picked for legibility on both dark and light themes; saturated
// enough to read at 3px wide, distinct from each other under quick glance.
const palette = [
  '#f59e0b', // amber
  '#3b82f6', // blue
  '#10b981', // emerald
  '#ec4899', // pink
  '#a855f7', // purple
  '#ef4444', // red
  '#06b6d4', // cyan
  '#84cc16', // lime
  '#f97316', // orange
  '#8b5cf6', // violet
]

// FNV-1a 32-bit. Stable, fast, no deps. Used purely as a palette index hash;
// not a security primitive.
function hash(input: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = (h + ((h << 1) + (h << 4) + (h << 7) + (h << 8) + (h << 24))) >>> 0
  }
  return h >>> 0
}

// hostColor returns a hex color for a remote host fingerprint, or null for
// the local host (caller should not render a stripe for local sessions).
export function hostColor(hostId: string | undefined, localId: string | undefined): string | null {
  if (!hostId) return null
  if (localId && hostId === localId) return null
  return palette[hash(hostId) % palette.length]
}
