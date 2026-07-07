import { useEffect, useMemo, useState } from 'react'
import type { FileArtifact } from '../hooks/useArtifacts'
import {
  grantArtifactToken,
  getArtifactKind,
  truncateArtifactText,
} from '../lib/artifactPreview'

interface ArtifactPreviewProps {
  artifact: FileArtifact
  sessionName: string
  hostId?: string
  onOpenFull: () => void
}

type PreviewStatus = 'loading' | 'loaded' | 'error'

export function ArtifactPreview({ artifact, sessionName, hostId, onOpenFull }: ArtifactPreviewProps) {
  const kind = useMemo(() => getArtifactKind(artifact.path, artifact.name), [artifact.name, artifact.path])
  const [status, setStatus] = useState<PreviewStatus>('loading')
  const [imageUrl, setImageUrl] = useState('')
  const [textPreview, setTextPreview] = useState('')
  const [isTruncated, setIsTruncated] = useState(false)

  useEffect(() => {
    const ac = new AbortController()
    let alive = true

    setStatus('loading')
    setImageUrl('')
    setTextPreview('')
    setIsTruncated(false)

    void (async () => {
      try {
        const token = await grantArtifactToken(artifact.path, sessionName, ac.signal, hostId)
        if (!alive || ac.signal.aborted) return
        const url = `/file?token=${encodeURIComponent(token)}`

        if (kind === 'image') {
          setImageUrl(url)
          return
        }

        const res = await fetch(url, { signal: ac.signal })
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        const text = await res.text()
        if (!alive || ac.signal.aborted) return
        const { preview, truncated } = truncateArtifactText(text)
        setTextPreview(preview)
        setIsTruncated(truncated)
        setStatus('loaded')
      } catch (err) {
        if (ac.signal.aborted || !alive) return
        console.error('Failed to load artifact preview:', err)
        setStatus('error')
      }
    })()

    return () => {
      alive = false
      ac.abort()
    }
  }, [artifact.path, kind, sessionName, hostId])

  return (
    <div className="border-t border-hairline bg-canvas/45 px-3 py-2.5">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-[10px] font-bold uppercase tracking-widest text-mute">Inline preview</span>
        <button
          type="button"
          onMouseDown={(e) => e.preventDefault()}
          onClick={onOpenFull}
          className="rounded-sm border border-hairline px-2 py-1 text-[10px] font-bold uppercase tracking-widest text-mute hover:text-ink hover:bg-surface transition-colors"
        >
          Open full file
        </button>
      </div>

      {status === 'loading' && (
        <div className="flex items-center justify-center gap-2 rounded-sm border border-hairline bg-surface/40 px-3 py-6 text-mute/70">
          <span className="inline-block h-2 w-2 rounded-full bg-mute/70 animate-spin" />
          <span className="text-[11px] font-bold uppercase tracking-widest">Loading preview…</span>
        </div>
      )}

      {status === 'error' && (
        <div className="rounded-sm border border-hairline bg-surface/30 px-3 py-4 text-[12px] text-destructive">
          Preview unavailable. File may be gone.
        </div>
      )}

      {kind === 'image' && imageUrl && (
        <div className="relative overflow-hidden rounded-sm border border-hairline bg-surface/30">
          <img
            src={imageUrl}
            alt={artifact.name || artifact.path}
            className="max-h-40 w-full object-contain bg-canvas"
            onLoad={() => setStatus('loaded')}
            onError={() => setStatus('error')}
          />
          {status === 'loading' && (
            <div className="absolute inset-0 flex items-center justify-center gap-2 bg-canvas/75 text-mute/70">
              <span className="inline-block h-2 w-2 rounded-full bg-mute/70 animate-spin" />
              <span className="text-[11px] font-bold uppercase tracking-widest">Loading preview…</span>
            </div>
          )}
        </div>
      )}

      {status === 'loaded' && kind === 'text' && (
        <div className="rounded-sm border border-hairline bg-canvas/60 overflow-hidden">
          <pre className="max-h-44 overflow-auto px-3 py-2 font-mono text-[11px] leading-[1.45] text-body-text whitespace-pre-wrap break-words">
            {textPreview || '(empty)'}
          </pre>
          {isTruncated && (
            <div className="border-t border-hairline px-3 py-2 text-[10px] italic text-mute/70">
              Truncated preview — click Open full file for more.
            </div>
          )}
        </div>
      )}
    </div>
  )
}
