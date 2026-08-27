import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { resolveLink, addFromLink, type ResolveResult } from '../lib/linkApi'
import { useSyncedPlaylists } from '../lib/syncedPlaylistApi'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { useToastStore } from '../lib/toastStore'
import { Button } from '../components/ui/Button'
import { Checkbox } from '../components/ui/Checkbox'
import { useSettings } from '../lib/settingsApi'
import { AUDIO_QUALITIES, DEFAULT_AUDIO_QUALITY, type AudioQuality } from '../lib/audioQuality'

export default function AddFromLink() {
  useDocumentTitle('Add from link')
  const navigate = useNavigate()
  const pushToast = useToastStore((s) => s.push)
  const { data: playlists } = useSyncedPlaylists()
  const { data: settings } = useSettings()

  const [url, setUrl] = useState('')
  const [preview, setPreview] = useState<ResolveResult | null>(null)
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

  async function onResolve() {
    const trimmed = url.trim()
    if (!trimmed) {
      setResolveError('Enter a URL')
      return
    }
    setResolveLoading(true)
    setResolveError(null)
    setAddError(null)
    setAddSuccess(null)
    try {
      const res = await resolveLink(trimmed)
      setPreview(res)
    } catch (e) {
      setPreview(null)
      if (e instanceof ApiError && e.status === 422) {
        setResolveError('Unsupported URL')
      } else if (e instanceof Error) {
        setResolveError(e.message)
      } else {
        setResolveError('Could not resolve link')
      }
    } finally {
      setResolveLoading(false)
    }
  }

  async function onAdd() {
    const trimmed = url.trim()
    if (!trimmed) {
      setAddError('Enter a URL')
      return
    }
    setAddLoading(true)
    setAddError(null)
    setAddSuccess(null)
    try {
      const res = await addFromLink(trimmed, {
        playlistId: selectedPlaylist || undefined,
        download: downloadNow,
        quality: effectiveQuality,
      })
      const target = res.playlistId || selectedPlaylist
      if (target) {
        pushToast('Added from link', 'success')
        navigate(`/playlist/${encodeURIComponent(target)}`)
      } else {
        pushToast('Added from link', 'success')
        if (res.catalogId) {
          setAddSuccess(`Added to canonical library: ${res.catalogId}`)
        } else {
          setAddSuccess('Added to canonical library')
        }
        if (res.job) {
          // job enqueued, already toasted
        }
      }
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 422) setAddError('Unsupported URL')
        else if (e.status === 404) setAddError('Playlist not found')
        else setAddError(e.message)
      } else if (e instanceof Error) {
        setAddError(e.message)
      } else {
        setAddError('Could not add from link')
      }
    } finally {
      setAddLoading(false)
    }
  }

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      <header>
        <h1 className="text-3xl font-black tracking-tight text-text-primary">Add from link</h1>
        <p className="mt-1 text-sm text-text-secondary">
          Add from link to your canonical library or a playlist. Downloads are source-native unless
          you pick a tier that transcodes down.
        </p>
      </header>

      <section className="rounded-lg border border-border-subtle bg-raised p-6 space-y-4">
        <h2 className="text-sm font-bold text-text-primary">Add from link</h2>

        <div className="space-y-2">
          <label htmlFor="add-link-url" className="text-sm font-semibold text-text-primary">
            Spotify or YouTube URL
          </label>
          <div className="flex gap-2">
            <input
              id="add-link-url"
              aria-label="Spotify or YouTube URL"
              placeholder="Paste Spotify or YouTube URL"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  void onResolve()
                }
              }}
              className="flex-1 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted outline-none focus:border-accent focus:ring-1 focus:ring-accent"
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
          {resolveError && (
            <p role="alert" className="text-sm text-error">
              {resolveError}
            </p>
          )}
        </div>

        {preview && (
          <div
            data-testid="preview-card"
            className="rounded-md border border-border-subtle bg-surface p-4 space-y-2"
          >
            <div className="flex gap-3">
              {preview.coverUrl ? (
                <img
                  src={preview.coverUrl}
                  alt={preview.title}
                  className="h-16 w-16 flex-none rounded-md object-cover bg-raised"
                />
              ) : (
                <div className="h-16 w-16 flex-none rounded-md bg-raised" data-testid="preview-cover-placeholder" />
              )}
              <div className="min-w-0 flex-1">
                <p className="text-sm font-bold text-text-primary truncate">{preview.title}</p>
                <p className="text-sm text-text-secondary truncate">{preview.artist}</p>
                {preview.album && <p className="text-xs text-text-muted truncate">{preview.album}</p>}
                <p className="mt-1 text-xs text-text-muted">
                  {preview.source} · {preview.kind}
                </p>
              </div>
            </div>
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
                    {q.label}
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
            aria-label="Add from link"
            disabled={addLoading}
            onClick={() => void onAdd()}
          >
            {addLoading ? 'Adding...' : 'Add from link'}
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
        </div>
      </section>
    </div>
  )
}
