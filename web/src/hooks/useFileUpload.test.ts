// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useFileUpload } from './useFileUpload'

type ResolvedUpload = { id: number; quotedPath: string | null }

// Mock XMLHttpRequest
class MockXMLHttpRequest {
  static instances: MockXMLHttpRequest[] = []

  readyState: number = 0
  status: number = 200
  responseText: string = ''
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  onabort: (() => void) | null = null
  upload: {
    onprogress: ((e: ProgressEvent) => void) | null
  }
  method: string = ''
  url: string = ''
  body: unknown = null

  constructor() {
    this.upload = { onprogress: null }
    MockXMLHttpRequest.instances.push(this)
  }

  open(method: string, url: string) {
    this.method = method
    this.url = url
  }

  send(body: unknown) {
    this.body = body
    // Test manually controls when onload fires
  }

  abort() {
    setTimeout(() => {
      if (this.onabort) this.onabort()
    }, 0)
  }
}

function flushTimers() {
  vi.runAllTimers()
}

beforeEach(() => {
  MockXMLHttpRequest.instances = []
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useFileUpload', () => {
  function installMock() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    ;(globalThis as any).XMLHttpRequest = MockXMLHttpRequest
  }

  it('constructs URL with session and filename, no host', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('mysession'))

    const file = new File(['hello'], 'test.txt', { type: 'text/plain' })
    const resolvedRef = { value: 'not-resolved' as string | null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r.quotedPath })
    })
    flushTimers()
    await act(async () => {})

    const xhr = MockXMLHttpRequest.instances[0]
    expect(xhr).toBeDefined()
    expect(xhr.method).toBe('POST')
    expect(xhr.url).toContain('/api/upload?')
    expect(xhr.url).toContain('session=mysession')
    expect(xhr.url).toContain('filename=test.txt')
    expect(xhr.url).not.toContain('host=')
  })

  it('includes host param when hostId is provided', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('mysession', 'peer-1'))

    const file = new File(['hello'], 'test.txt', { type: 'text/plain' })
    await act(async () => {
      result.current.uploadFile(file)
    })
    flushTimers()
    await act(async () => {})

    const xhr = MockXMLHttpRequest.instances[0]
    expect(xhr.url).toContain('host=peer-1')
  })

  it('URL-encodes session name and filename', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('my session', 'peer&1'))

    const file = new File(['hello'], 'file name.txt', { type: 'text/plain' })
    await act(async () => {
      result.current.uploadFile(file)
    })
    flushTimers()
    await act(async () => {})

    const xhr = MockXMLHttpRequest.instances[0]
    expect(xhr.url).toContain('session=my+session')
    expect(xhr.url).toContain('filename=file+name.txt')
    expect(xhr.url).toContain('host=peer%261')
  })

  it('reports upload progress', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['x'.repeat(1000)], 'big.txt', { type: 'text/plain' })
    await act(async () => {
      result.current.uploadFile(file)
    })

    // Verify initial state
    expect(result.current.uploads).toHaveLength(1)
    expect(result.current.uploads[0].status).toBe('uploading')
    expect(result.current.uploads[0].sent).toBe(0)

    // Simulate progress event
    const xhr = MockXMLHttpRequest.instances[0]
    await act(async () => {
      xhr.upload.onprogress?.(new ProgressEvent('progress', { loaded: 500, total: 1000, lengthComputable: true }))
    })

    expect(result.current.uploads[0].sent).toBe(500)
    expect(result.current.uploads[0].size).toBe(1000)

    // Let the XHR complete
    flushTimers()
    await act(async () => {})
  })

  it('marks done and resolves quotedPath on 200', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'ok.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 200
    xhr.responseText = JSON.stringify({ path: '/tmp/p', quotedPath: "'/tmp/p'" })
    await act(async () => {
      xhr.onload?.()
    })

    expect(result.current.uploads[0].status).toBe('done')
    expect(result.current.uploads[0].quotedPath).toBe("'/tmp/p'")
    expect(resolvedRef.value?.quotedPath).toBe("'/tmp/p'")

    // Auto-dismiss after 4s
    expect(result.current.uploads).toHaveLength(1)
    await act(async () => {
      vi.advanceTimersByTime(4000)
    })
    expect(result.current.uploads).toHaveLength(0)
  })

  it('marks error on non-200 status', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'bad.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 500
    xhr.responseText = 'Internal Server Error'
    await act(async () => {
      xhr.onload?.()
    })

    expect(result.current.uploads[0].status).toBe('error')
    expect(result.current.uploads[0].error).toBe('Internal Server Error')
    expect(resolvedRef.value?.quotedPath).toBeNull()

    // Error items persist (not auto-dismissed)
    await act(async () => {
      vi.advanceTimersByTime(4000)
    })
    expect(result.current.uploads).toHaveLength(1)
  })

  it('marks cancelled on xhr.abort() via cancelUpload', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'cancel.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const id = result.current.uploads[0].id
    await act(async () => {
      result.current.cancelUpload(id)
    })

    // Trigger onabort
    const xhr = MockXMLHttpRequest.instances[0]
    await act(async () => {
      xhr.onabort?.()
    })

    expect(result.current.uploads[0].status).toBe('cancelled')
    expect(resolvedRef.value?.quotedPath).toBeNull()
  })

  it('dismissUpload removes the upload item', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'dismiss.txt', { type: 'text/plain' })
    await act(async () => {
      result.current.uploadFile(file)
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 500
    xhr.responseText = 'fail'
    await act(async () => {
      xhr.onload?.()
    })

    expect(result.current.uploads).toHaveLength(1)

    const id = result.current.uploads[0].id
    await act(async () => {
      result.current.dismissUpload(id)
    })
    expect(result.current.uploads).toHaveLength(0)
  })

  it('keepVisible cancels auto-dismiss and sets injectionSkipped', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'keep.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 200
    xhr.responseText = JSON.stringify({ path: '/tmp/p', quotedPath: "'/tmp/p'" })
    await act(async () => {
      xhr.onload?.()
    })

    // Item is done
    expect(result.current.uploads[0].status).toBe('done')

    // Call keepVisible to cancel auto-dismiss
    const uploadId = resolvedRef.value?.id ?? 0
    await act(async () => {
      result.current.keepVisible(uploadId)
    })

    expect(result.current.uploads[0].injectionSkipped).toBe(true)

    // Should not auto-dismiss after 4s
    await act(async () => {
      vi.advanceTimersByTime(4000)
    })
    expect(result.current.uploads).toHaveLength(1)
  })

  it('handles onerror as network error', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'net.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    await act(async () => {
      xhr.onerror?.()
    })

    expect(result.current.uploads[0].status).toBe('error')
    expect(result.current.uploads[0].error).toBe('Network error')
    expect(resolvedRef.value?.quotedPath).toBeNull()
  })

  it('handles invalid JSON response as error', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'badjson.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 200
    xhr.responseText = 'not json'
    await act(async () => {
      xhr.onload?.()
    })

    expect(result.current.uploads[0].status).toBe('error')
    expect(result.current.uploads[0].error).toBe('Invalid response')
    expect(resolvedRef.value?.quotedPath).toBeNull()
  })

  it('empty quotedPath in response resolves as empty string', async () => {
    installMock()
    const { result } = renderHook(() => useFileUpload('s'))

    const file = new File(['hello'], 'emptyq.txt', { type: 'text/plain' })
    const resolvedRef: { value: ResolvedUpload | null } = { value: null }
    await act(async () => {
      result.current.uploadFile(file).then((r: ResolvedUpload) => { resolvedRef.value = r })
    })

    const xhr = MockXMLHttpRequest.instances[0]
    xhr.status = 200
    xhr.responseText = JSON.stringify({ path: '/tmp/p' })
    await act(async () => {
      xhr.onload?.()
    })

    expect(result.current.uploads[0].status).toBe('done')
    // quotedPath defaults to '' when missing, and uploadFiles checks truthiness
    expect(resolvedRef.value?.quotedPath).toBe('')
  })
})
