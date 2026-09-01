import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Modal } from './ui'
import { renameAlbum, renameArtist } from '../lib/libraryEditApi'

interface RenameEntityDialogProps {
  kind: 'album' | 'artist'
  id: string
  /** The name as it currently shows, which is what the field starts from. */
  currentName: string
  onClose: () => void
}

/**
 * Renames one album or artist. The name cascades onto every track underneath
 * it; nothing is written to the files, so Navidrome keeps the original tags.
 * Clearing the field restores the library's own name.
 */
export function RenameEntityDialog({ kind, id, currentName, onClose }: RenameEntityDialogProps) {
  const qc = useQueryClient()
  const [name, setName] = useState(currentName)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function handleSave() {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const rename = kind === 'album' ? renameAlbum : renameArtist
      await rename(id, name.trim())
      await qc.invalidateQueries({ queryKey: ['library'] })
      await qc.invalidateQueries({ queryKey: ['album-detail'] })
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : `Couldn't rename this ${kind}`)
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      testId="rename-entity-dialog"
      title={kind === 'album' ? 'Rename album' : 'Rename artist'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" onClick={() => void handleSave()} disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
        </>
      }
    >
      <div className="space-y-1.5">
        <label htmlFor="rename-entity-name" className="block text-sm font-semibold text-text-primary">
          Name
        </label>
        <input
          id="rename-entity-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void handleSave()
            }
          }}
          disabled={busy}
          className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50"
        />
      </div>

      <p className="text-xs text-text-muted">
        This changes the name in Reverb only — your files and Navidrome are left untouched. Clear
        the field to go back to the original name.
      </p>

      {error && (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      )}
    </Modal>
  )
}
