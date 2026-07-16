import { useRef, useState, useCallback, useEffect } from 'react'

export type UploadStatus = 'uploading' | 'done' | 'error' | 'cancelled'

export interface UploadItem {
  id: number
  name: string
  size: number
  sent: number
  status: UploadStatus
  error?: string
  quotedPath?: string
  injectionSkipped?: boolean
}

let nextUploadId = 0

export function useFileUpload(sessionName: string, hostId?: string) {
  const [uploads, setUploads] = useState<UploadItem[]>([])
  const xhrMap = useRef<Map<number, XMLHttpRequest>>(new Map())
  const dismissTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())

  // Cleanup timers on unmount
  useEffect(() => {
    return () => {
      for (const timer of dismissTimers.current.values()) {
        clearTimeout(timer)
      }
      dismissTimers.current.clear()
    }
  }, [])

  const cancelUpload = useCallback((id: number) => {
    const xhr = xhrMap.current.get(id)
    if (xhr) {
      xhr.abort()
      xhrMap.current.delete(id)
    }
  }, [])

  const dismissUpload = useCallback((id: number) => {
    const timer = dismissTimers.current.get(id)
    if (timer) {
      clearTimeout(timer)
      dismissTimers.current.delete(id)
    }
    setUploads(prev => prev.filter(u => u.id !== id))
  }, [])

  const keepVisible = useCallback((id: number) => {
    const timer = dismissTimers.current.get(id)
    if (timer) {
      clearTimeout(timer)
      dismissTimers.current.delete(id)
    }
    setUploads(prev => prev.map(u =>
      u.id === id ? { ...u, injectionSkipped: true } : u
    ))
  }, [])

  const uploadFile = useCallback((file: File): Promise<{ id: number; quotedPath: string | null }> => {
    const id = ++nextUploadId
    const initialItem: UploadItem = {
      id,
      name: file.name,
      size: file.size,
      sent: 0,
      status: 'uploading',
    }
    setUploads(prev => [...prev, initialItem])

    return new Promise((resolve) => {
      const xhr = new XMLHttpRequest()
      xhrMap.current.set(id, xhr)

      const params = new URLSearchParams({
        session: sessionName,
        filename: file.name,
      })
      if (hostId) params.set('host', hostId)

      xhr.open('POST', `/api/upload?${params.toString()}`)

      xhr.upload.onprogress = (e: ProgressEvent) => {
        if (e.lengthComputable) {
          setUploads(prev => prev.map(u =>
            u.id === id ? { ...u, sent: e.loaded, size: e.total } : u
          ))
        }
      }

      xhr.onload = () => {
        xhrMap.current.delete(id)
        if (xhr.status === 200) {
          try {
            const data = JSON.parse(xhr.responseText) as { path?: string; quotedPath?: string }
            const quotedPath = data.quotedPath || ''
            setUploads(prev => prev.map(u =>
              u.id === id ? { ...u, status: 'done', sent: u.size, quotedPath } : u
            ))
            // Auto-dismiss after 4s
            const timer = setTimeout(() => {
              dismissTimers.current.delete(id)
              setUploads(prev => prev.filter(u => u.id !== id))
            }, 4000)
            dismissTimers.current.set(id, timer)
            resolve({ id, quotedPath })
          } catch {
            setUploads(prev => prev.map(u =>
              u.id === id ? { ...u, status: 'error', error: 'Invalid response' } : u
            ))
            resolve({ id, quotedPath: null })
          }
        } else {
          const errorMsg = xhr.responseText?.trim().slice(0, 200) || `HTTP ${xhr.status}`
          setUploads(prev => prev.map(u =>
            u.id === id ? { ...u, status: 'error', error: errorMsg } : u
          ))
          resolve({ id, quotedPath: null })
        }
      }

      xhr.onerror = () => {
        xhrMap.current.delete(id)
        setUploads(prev => prev.map(u =>
          u.id === id ? { ...u, status: 'error', error: 'Network error' } : u
        ))
        resolve({ id, quotedPath: null })
      }

      xhr.onabort = () => {
        xhrMap.current.delete(id)
        setUploads(prev => prev.map(u =>
          u.id === id ? { ...u, status: 'cancelled', sent: u.sent } : u
        ))
        resolve({ id, quotedPath: null })
      }

      xhr.send(file)
    })
  }, [sessionName, hostId])

  return { uploads, uploadFile, cancelUpload, dismissUpload, keepVisible }
}
