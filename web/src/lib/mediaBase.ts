/**
 * Origin to load audio from.
 *
 * In the browser this is "" (same-origin, relative URLs). In the Wails desktop
 * app the SPA is served from the custom `wails://` scheme, and WebKitGTK loads
 * media through GStreamer, which cannot read app-registered URI schemes — an
 * `<audio>` pointed at `wails://wails/api/v1/stream/...` silently never loads,
 * leaving the player stuck at 0:00. Wails also serves scheme requests from a
 * single goroutine, so a long-lived stream body would stall every other asset
 * request.
 *
 * So audio dials the plain 127.0.0.1 listener directly, the same escape hatch
 * the WebSocket already uses — see handleRuntimeConfig and realtime.ts. The API
 * is owner-authenticated and loopback-only (requireAuth injects the local owner
 * for browser requests; paired devices use sync tokens/P2P), so the
 * cross-origin GET needs no credentials.
 */
export function mediaBase(): string {
  if (typeof window === 'undefined') return ''
  const port = window.__REVERB_PORT__
  return port ? `http://127.0.0.1:${port}` : ''
}
