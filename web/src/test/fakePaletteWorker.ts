import { vi } from 'vitest'
import type { RGB } from '../lib/palette'

// FakePaletteWorker stands in for paletteWorker.ts (jsdom has no Worker or
// OffscreenCanvas). It speaks the same { id, coverUrl } → { id, rgb | error }
// protocol, and `extract` decides each reply so tests can hold, reorder or fail it.
export class FakePaletteWorker {
  static extract: (coverUrl: string) => Promise<RGB> = async () => [0, 0, 0]
  static requests: string[] = []

  onmessage: ((e: MessageEvent) => void) | null = null

  postMessage({ id, coverUrl }: { id: string; coverUrl: string }): void {
    FakePaletteWorker.requests.push(coverUrl)
    FakePaletteWorker.extract(coverUrl).then(
      (rgb) => this.onmessage?.({ data: { id, rgb } } as MessageEvent),
      (err: Error) => this.onmessage?.({ data: { id, error: err.message } } as MessageEvent),
    )
  }
}

// installFakePaletteWorker stubs the global Worker and resets the module graph so
// the next import of paletteService starts with an empty cache and no worker.
export function installFakePaletteWorker(extract: (coverUrl: string) => Promise<RGB>): void {
  FakePaletteWorker.extract = extract
  FakePaletteWorker.requests = []
  vi.stubGlobal('Worker', FakePaletteWorker)
  vi.resetModules()
}
