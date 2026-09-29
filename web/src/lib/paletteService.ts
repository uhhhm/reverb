import type { RGB } from './palette'

const cache = new Map<string, RGB>()
const inflight = new Map<string, Promise<RGB>>()

// worker is created lazily on first real use so importing this module never spins a
// Worker (which would break jsdom and SSR).
let worker: Worker | null = null
let nextId = 0
const pending = new Map<string, { resolve: (v: RGB) => void; reject: (e: Error) => void }>()

function ensureWorker(): Worker {
  if (worker) return worker
  worker = new Worker(new URL('./paletteWorker.ts', import.meta.url), { type: 'module' })
  worker.onmessage = (e: MessageEvent<{ id: string; rgb?: RGB; error?: string }>) => {
    const { id, rgb, error } = e.data
    const p = pending.get(id)
    if (!p) return
    pending.delete(id)
    if (rgb) p.resolve(rgb)
    else p.reject(new Error(error ?? 'palette failed'))
  }
  return worker
}

function computeViaWorker(coverUrl: string): Promise<RGB> {
  const id = String(nextId++)
  const w = ensureWorker()
  return new Promise<RGB>((resolve, reject) => {
    pending.set(id, { resolve, reject })
    w.postMessage({ id, coverUrl })
  })
}

// getPalette resolves the dominant color for a cover URL, computing it exactly once
// per URL (cache + in-flight de-dup).
export function getPalette(coverUrl: string): Promise<RGB> {
  const cached = cache.get(coverUrl)
  if (cached) return Promise.resolve(cached)
  const existing = inflight.get(coverUrl)
  if (existing) return existing

  const promise = computeViaWorker(coverUrl)
    .then((rgb) => {
      cache.set(coverUrl, rgb)
      inflight.delete(coverUrl)
      return rgb
    })
    .catch((err) => {
      inflight.delete(coverUrl)
      throw err
    })
  inflight.set(coverUrl, promise)
  return promise
}
