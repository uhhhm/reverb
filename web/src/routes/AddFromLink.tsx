import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { resolveLink, addFromLinksBatch, type ResolveResult, type LinkOptions } from '../lib/linkApi'
import { useSyncedPlaylists } from '../lib/syncedPlaylistApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { parseLinks } from '../lib/parseLinks'
import { LinkOptionsPanel } from '../components/LinkOptionsPanel'
import { isYouTubeLink } from '../lib/parseLinks'
import { useToastStore } from '../lib/toastStore'
import { Button } from '../components/ui/Button'
import { Checkbox } from '../components/ui/Checkbox'
import { useSettings } from '../lib/settingsApi'
import { AUDIO_QUALITIES, DEFAULT_AUDIO_QUALITY, qualityOptionLabel, type AudioQuality } from '../lib/audioQuality'

interface LinkPreview {
  url: string
  result?: ResolveResult
  error?: string
}

interface LinkOutcome {
  url: string
  ok: boolean
  message: string
}

function describeError(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    if (e.status === 422) return 'Unsupported URL'
    if (e.status === 404) return 'Playlist not found'
    return e.message
  }
  return e instanceof Error ? e.message : fallback
}

export default function AddFromLink() {
  useDocumentTitle('Add from link')
  const navigate = useNavigate()
  const pushToast = useToastStore((s) => s.push)
  const { data: playlists } = useSyncedPlaylists()
  const { data: settings } = useSettings()

  const [urls, setUrls] = useState('')
  const [previews, setPreviews] = useState<LinkPreview[]>([])
  const [resolveLoading, setResolveLoading] = useState(false)
  const [resolveError, setResolveError] = useState<string | null>(null)

  const [selectedPlaylist, setSelectedPlaylist] = useState('')
  const [downloadNow, setDownloadNow] = useState(true)
  // Empty means "follow the configured default"; the select seeds from settings
  // once they load so the user can see what they are about to get.
  const [quality, setQuality] = useState<AudioQuality | ''>('')
  const effectiveQuality: AudioQuality = quality || settings?.downloadQuality || DEFAULT_AUDIO_QUALITY

  const [addLoading, setAddLoading] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [addSuccess, setAddSuccess] = useState<string | null>(null)
  const [addResults, setAddResults] = useState<LinkOutcome[]>([])
  // Per-link trim/split options, keyed by the link itself so editing the
  // textarea does not shuffle settings onto the wrong video.
  const [linkOptions, setLinkOptions] = useState<Record<string, LinkOptions>>({})

  const parsedUrls = parseLinks(urls)
  const addLabel = addLoading
    ? `Adding${addResults.length ? ` ${addResults.length}/${parsedUrls.length}` : ''}...`
    : parsedUrls.length > 1
      ? `Add ${parsedUrls.length} links`
      : 'Add from link'

  async function onResolve() {
    const links = parseLinks(urls)
    if (links.length === 0) {
      setResolveError('Enter a URL')
      return
    }
    setResolveLoading(true)
    setResolveError(null)
    setAddError(null)
    setAddSuccess(null)
    setAddResults([])
    // Resolve concurrently — each link stands alone, so one bad URL must not
    // hide the previews for the rest.
    const settled = await Promise.all(
      links.map(async (link): Promise<LinkPreview> => {
        try {
          return { url: link, result: await resolveLink(link) }
        } catch (e) {
          return { url: link, error: describeError(e, 'Could not resolve link') }
        }
      }),
    )
    setPreviews(settled)
    setResolveLoading(false)
  }

  async function onAdd() {
    const links = parseLinks(urls)
    if (links.length === 0) {
      setAddError('Enter a URL')
      return
    }
    setAddLoading(true)
    setAddError(null)
    setAddSuccess(null)
    setAddResults([])

    // One POST per batch — planner fans out per-link work server-side so the
    // client avoids N round-trips and the server can batch catalog/sync work.
    const items = links.map((link) => ({
      url: link,
      playlistId: selectedPlaylist || undefined,
      download: downloadNow,
      quality: effectiveQuality,
      ...(linkOptions[link] ?? {}),
    }))
    let batchResults: Awaited<ReturnType<typeof addFromLinksBatch>> | null = null
    let batchError: unknown = null
    try {
      batchResults = await addFromLinksBatch(items)
    } catch (e) {
      batchError = e
    }
    const outcomes: LinkOutcome[] = []
    let lastPlaylistId = ''
    if (batchResults) {
      for (const r of batchResults.results) {
        if (r.error) {
          outcomes.push({ url: r.url, ok: false, message: r.error })
        } else {
          const target = r.playlistId || selectedPlaylist
          if (target) lastPlaylistId = target
          const chapterCount = Array.isArray(r.jobs) ? r.jobs.length : 0
          const where = target ? 'Added to playlist' : 'Added to library'
          outcomes.push({
            url: r.url,
            ok: true,
            message: chapterCount > 1 ? `${where} as ${chapterCount} chapters` : where,
          })
        }
      }
    } else {
      // Batch request itself failed — mark every link as failed.
      for (const link of links) {
        outcomes.push({ url: link, ok: false, message: describeError(batchError, 'Could not add from link') })
      }
    }
    setAddResults(outcomes)
    setAddLoading(false)

    const added = outcomes.filter((o) => o.ok).length
    const failed = outcomes.length - added
    if (added > 0) {
      pushToast(
        failed === 0
          ? `Added ${added} link${added === 1 ? '' : 's'}`
          : `Added ${added} of ${outcomes.length} links`,
        failed === 0 ? 'success' : 'error',
      )
    } else {
      pushToast('Nothing could be added', 'error')
    }
    if (failed > 0) setAddError(`${failed} link${failed === 1 ? '' : 's'} failed — see below.`)

    // Only follow a single-link add through to its playlist; on a batch the
    // results list is the more useful thing to stay on.
    if (added > 0 && outcomes.length === 1 && lastPlaylistId) {
      navigate(`/playlist/${encodeURIComponent(lastPlaylistId)}`)
    } else if (added > 0) {
      setAddSuccess(`Added ${added} link${added === 1 ? '' : 's'} to your library.`)
    }
  }

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Add from link</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Add one or many links to your canonical library or a playlist. Downloads are
          source-native unless you pick a tier that transcodes down.
        </p>
      </header>

      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <h2 className="text-sm font-bold text-text-primary">Add from link</h2>

        <div className="space-y-2">
          <label htmlFor="add-link-url" className="text-sm font-semibold text-text-primary">
            Spotify or YouTube URLs
          </label>
          <div className="flex gap-2">
            <textarea
              id="add-link-url"
              aria-label="Spotify or YouTube URLs"
              placeholder={'Paste one link per line\nhttps://open.spotify.com/track/...\nhttps://youtube.com/watch?v=...'}
              rows={4}
              value={urls}
              onChange={(e) => setUrls(e.target.value)}
              onKeyDown={(e) => {
                // Enter inserts a newline (links are one per line); Ctrl/Cmd+Enter resolves.
                if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault()
                  void onResolve()
                }
              }}
              className="flex-1 resize-y rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
            />
            <Button
              variant="secondary"
              size="sm"
              aria-label="Resolve"
              disabled={resolveLoading}
              onClick={() => void onResolve()}
            >
              {resolveLoading ? 'Resolving...' : 'Resolve'}
            </Button>
          </div>
          <p className="text-xs text-text-muted">
            {parsedUrls.length === 0
              ? 'One link per line — spaces and commas work too.'
              : `${parsedUrls.length} link${parsedUrls.length === 1 ? '' : 's'} ready.`}
          </p>

          {/* Per-link trim / chapter options. YouTube only — nothing else has a
              timeline Reverb can address. */}
          {parsedUrls.some(isYouTubeLink) && (
            <div className="space-y-2 pt-1" data-testid="link-options">
              {parsedUrls.filter(isYouTubeLink).map((link) => (
                <div key={link} className="space-y-1">
                  <p className="truncate text-xs text-text-muted">{link}</p>
                  <LinkOptionsPanel
                    url={link}
                    value={linkOptions[link] ?? {}}
                    onChange={(next) => setLinkOptions((prev) => ({ ...prev, [link]: next }))}
                  />
                </div>
              ))}
            </div>
          )}
          {resolveError && (
            <p role="alert" className="text-sm text-error">
              {resolveError}
            </p>
          )}
        </div>

        {previews.length > 0 && (
          <div className="space-y-2" data-testid="preview-list">
            {previews.map((p) => (
              <div
                key={p.url}
                data-testid="preview-card"
                className="rounded-md border border-border-subtle bg-surface p-4"
              >
                {p.result ? (
                  <div className="flex gap-3">
                    {p.result.coverUrl ? (
                      <img
                        src={p.result.coverUrl}
                        alt={p.result.title}
                        className="h-16 w-16 flex-none rounded-md object-cover bg-raised"
                      />
                    ) : (
                      <div className="h-16 w-16 flex-none rounded-md bg-raised" data-testid="preview-cover-placeholder" />
                    )}
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-bold text-text-primary truncate">{p.result.title}</p>
                      <p className="text-sm text-text-secondary truncate">{p.result.artist}</p>
                      {p.result.album && <p className="text-xs text-text-muted truncate">{p.result.album}</p>}
                      <p className="mt-1 text-xs text-text-muted">
                        {p.result.source} · {p.result.kind}
                      </p>
                    </div>
                  </div>
                ) : (
                  <div className="min-w-0">
                    <p className="truncate text-sm text-text-secondary">{p.url}</p>
                    <p role="alert" className="mt-1 text-sm text-error">
                      {p.error}
                    </p>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}

        <div className="space-y-3">
          <div className="space-y-1">
            <label htmlFor="playlist-select" className="text-sm font-semibold text-text-primary">
              Add to playlist
            </label>
            <select
              id="playlist-select"
              aria-label="Add to playlist"
              value={selectedPlaylist}
              onChange={(e) => setSelectedPlaylist(e.target.value)}
              className="w-full appearance-none rounded-md border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary outline-none focus:border-accent focus:ring-1 focus:ring-accent"
            >
              <option value="">Add to library only</option>
              {playlists?.map((pl) => (
                <option key={pl.id} value={pl.id}>
                  {pl.name}
                </option>
              ))}
            </select>
            <p className="text-xs text-text-muted">Choose a playlist or add to your canonical library.</p>
          </div>

          <label className="flex items-center gap-2">
            <Checkbox label="Download now" checked={downloadNow} onChange={setDownloadNow} />
            <span className="text-sm font-semibold text-text-primary">Download now</span>
          </label>

          {downloadNow && (
            <div className="space-y-1">
              <label htmlFor="quality-select" className="text-sm font-semibold text-text-primary">
                Audio quality
              </label>
              <select
                id="quality-select"
                aria-label="Audio quality"
                value={effectiveQuality}
                onChange={(e) => setQuality(e.target.value as AudioQuality)}
                className="w-full appearance-none rounded-md border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary outline-none focus:border-accent focus:ring-1 focus:ring-accent"
              >
                {AUDIO_QUALITIES.map((q) => (
                  <option key={q.value} value={q.value}>
                    {qualityOptionLabel(q.value)}
                  </option>
                ))}
              </select>
              <p className="text-xs text-text-muted">
                {AUDIO_QUALITIES.find((q) => q.value === effectiveQuality)?.hint}
              </p>
            </div>
          )}
          <p className="text-xs text-text-secondary">
            The download runs on the server device and the result syncs to your canonical library.
          </p>
        </div>

        <div className="space-y-2">
          <Button
            variant="primary"
            size="sm"
            aria-label={addLabel}
            disabled={addLoading || parsedUrls.length === 0}
            onClick={() => void onAdd()}
          >
            {addLabel}
          </Button>
          {addError && (
            <p role="alert" className="text-sm text-error">
              {addError}
            </p>
          )}
          {addSuccess && (
            <p role="status" className="text-sm text-green-400">
              {addSuccess}
            </p>
          )}
          {addResults.length > 0 && (
            <ul className="space-y-1" data-testid="add-results">
              {addResults.map((r) => (
                <li key={r.url} className="flex gap-2 text-xs">
                  <span className={r.ok ? 'text-green-400' : 'text-error'} aria-hidden="true">
                    {r.ok ? '✓' : '✕'}
                  </span>
                  <span className="min-w-0 flex-1 truncate text-text-secondary">{r.url}</span>
                  <span className={r.ok ? 'text-text-muted' : 'text-error'}>{r.message}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </div>
  )
}
