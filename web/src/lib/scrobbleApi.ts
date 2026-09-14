import { api, ApiError } from './api'

// ── Error type ────────────────────────────────────────────────────────────────

export type ScrobbleErrorCode =
  | 'lastfm_not_configured'
  | 'lastfm_unavailable'
  | 'invalid_token'
  | 'listenbrainz_unavailable'

export class ScrobbleError extends Error {
  code: ScrobbleErrorCode

  constructor(code: ScrobbleErrorCode, message: string) {
    super(message)
    this.name = 'ScrobbleError'
    this.code = code
  }
}

// ── Shared types ──────────────────────────────────────────────────────────────

export interface ScrobbleLink {
  provider: string
  username: string
  status: string
}

export interface LinksResult {
  configured: boolean
  links: ScrobbleLink[]
}

// ── Per-user scrobble API ─────────────────────────────────────────────────────

/** GET /api/v1/scrobble/links → { configured, links } */
export function getLinks(): Promise<LinksResult> {
  return api.get<LinksResult>('/scrobble/links')
}

/**
 * POST /api/v1/scrobble/lastfm/auth-url → { authUrl, token }
 *
 * Throws ScrobbleError with code:
 *   - 'lastfm_not_configured' on 400 { error: "lastfm_not_configured" }
 *   - 'lastfm_unavailable'    on any other error (5xx, network, etc.)
 */
export async function lastfmAuthUrl(): Promise<{ authUrl: string; token: string }> {
  try {
    return await api.post<{ authUrl: string; token: string }>('/scrobble/lastfm/auth-url')
  } catch (e) {
    if (e instanceof ApiError) {
      if (
        e.status === 400 &&
        e.body &&
        (e.body as Record<string, unknown>).error === 'lastfm_not_configured'
      ) {
        throw new ScrobbleError('lastfm_not_configured', 'Last.fm is not configured on this server')
      }
      throw new ScrobbleError('lastfm_unavailable', 'Last.fm is temporarily unavailable')
    }
    throw new ScrobbleError('lastfm_unavailable', 'Last.fm is temporarily unavailable')
  }
}

/** POST /api/v1/scrobble/lastfm/complete { token } → { username } */
export function lastfmComplete(token: string): Promise<{ username: string }> {
  return api.post<{ username: string }>('/scrobble/lastfm/complete', { token })
}

/** DELETE /api/v1/scrobble/lastfm → void */
export function lastfmDisconnect(): Promise<void> {
  return api.del<void>('/scrobble/lastfm')
}

/**
 * PUT /api/v1/scrobble/listenbrainz { token } → { username }
 *
 * Throws ScrobbleError with code 'invalid_token' when ListenBrainz rejects the
 * token, and 'listenbrainz_unavailable' on any other error.
 */
export async function listenbrainzConnect(token: string): Promise<{ username: string }> {
  try {
    return await api.put<{ username: string }>('/scrobble/listenbrainz', { token })
  } catch (e) {
    if (
      e instanceof ApiError &&
      e.status === 400 &&
      e.body &&
      (e.body as Record<string, unknown>).error === 'invalid_token'
    ) {
      throw new ScrobbleError('invalid_token', 'ListenBrainz rejected the token')
    }
    throw new ScrobbleError('listenbrainz_unavailable', 'ListenBrainz is temporarily unavailable')
  }
}

/** DELETE /api/v1/scrobble/listenbrainz → void. Stops uploads. */
export function listenbrainzDisconnect(): Promise<void> {
  return api.del<void>('/scrobble/listenbrainz')
}

/** POST /api/v1/scrobble/nowplaying → void (fire-and-forget) */
export function nowPlaying(track: {
  title: string
  artist: string
  album: string
  durationMs: number
}): Promise<void> {
  return api.post<void>('/scrobble/nowplaying', track)
}

// ── Admin: Last.fm app-key configuration ─────────────────────────────────────

/** GET /api/v1/admin/integrations/lastfm → { apiKey, apiSecretSet } */
export function getLastfmConfig(): Promise<{ apiKey: string; apiSecretSet: boolean }> {
  return api.get<{ apiKey: string; apiSecretSet: boolean }>('/admin/integrations/lastfm')
}

/** PUT /api/v1/admin/integrations/lastfm { apiKey, apiSecret } → void */
export function setLastfmConfig(cfg: { apiKey: string; apiSecret: string }): Promise<void> {
  return api.put<void>('/admin/integrations/lastfm', cfg)
}
