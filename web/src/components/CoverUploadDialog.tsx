import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Modal } from './ui'
import { clearCovers, uploadCovers, type CoverTarget } from '../lib/libraryEditApi'

/** Matches the server's limit; checked here so an oversized file never uploads. */
const MAX_BYTES = 5 * 1024 * 1024
const ACCEPTED = ['image/jpeg', 'image/png', 'image/webp']

interface CoverUploadDialogProps {
  /** The albums and tracks the image will be applied to. */
  targets: CoverTarget[]
  onClose: () => void
  onApplied?: () => void
}

/**
 * Uploads one image as the cover for one or many albums and tracks.
 *
 * The image is stored beside Reverb's database and swapped in when the library
 * is read, so the audio files and whatever art is embedded in them are left
 * alone. Removing the upload brings the library's own art back.
 */
export function CoverUploadDialog({ targets, onClose, onApplied }: CoverUploadDialogProps) {
  const qc = useQueryClient()
  const [file, setFile] = useState<File | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // An object URL is a resource, not a string, so it is revoked when the file
  // is replaced or the dialog unmounts.
  const preview = useMemo(() => (file ? URL.createObjectURL(file) : null), [file])
  useEffect(() => {
    if (!preview) return
    return () => URL.revokeObjectURL(preview)
  }, [preview])

  function choose(next: File | null) {
    if (!next) {
      setFile(null)
      return
    }
    if (!ACCEPTED.includes(next.type)) {
      setError('That file is not a JPEG, PNG, or WebP.')
      return
    }
    if (next.size > MAX_BYTES) {
      setError('That image is larger than 5 MB.')
      return
    }
    setError(null)
    setFile(next)
  }

  async function refresh() {
    await qc.invalidateQueries({ queryKey: ['library'] })
    await qc.invalidateQueries({ queryKey: ['album-detail'] })
    await qc.invalidateQueries({ queryKey: ['synced-playlist'] })
  }

  async function handleApply() {
    if (busy || !file || targets.length === 0) return
    setBusy(true)
    setError(null)
    try {
      await uploadCovers(file, targets)
      await refresh()
      onApplied?.()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't upload this cover")
      setBusy(false)
    }
  }

  async function handleClear() {
    if (busy || targets.length === 0) return
    setBusy(true)
    setError(null)
    try {
      await clearCovers(targets)
      await refresh()
      onApplied?.()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't remove the cover")
      setBusy(false)
    }
  }

  const count = targets.length

  return (
    <Modal
      open
      onClose={onClose}
      testId="cover-upload-dialog"
      title={count === 1 ? 'Set cover art' : `Set cover art for ${count} items`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="secondary" onClick={() => void handleClear()} disabled={busy}>
            Remove cover
          </Button>
          <Button variant="primary" onClick={() => void handleApply()} disabled={busy || !file}>
            {busy ? 'Uploading…' : 'Apply'}
          </Button>
        </>
      }
    >
      <div className="space-y-1.5">
        <label htmlFor="cover-file" className="block text-sm font-semibold text-text-primary">
          Image
        </label>
        <input
          id="cover-file"
          type="file"
          accept={ACCEPTED.join(',')}
          onChange={(e) => choose(e.target.files?.[0] ?? null)}
          className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary file:mr-3 file:rounded-md file:border-0 file:bg-raised-hover file:px-3 file:py-1 file:text-sm file:text-text-primary"
        />
        <p className="text-xs text-text-muted">JPEG, PNG, or WebP, up to 5 MB.</p>
      </div>

      {preview && (
        <img
          src={preview}
          alt="Selected cover preview"
          className="mx-auto aspect-square w-40 rounded-lg object-cover"
        />
      )}

      {count > 1 && (
        <p className="text-sm text-text-muted">
          This image will be used for all {count} selected items.
        </p>
      )}

      <p className="text-xs text-text-muted">
        The image is stored in Reverb only — your audio files and Navidrome are left untouched.
      </p>

      {error && (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      )}
    </Modal>
  )
}
