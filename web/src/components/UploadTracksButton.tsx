import { useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button } from './ui'
import { uploadTracks, UPLOAD_EXTENSIONS } from '../lib/uploadApi'
import { useToastStore } from '../lib/toastStore'

/**
 * Adds local audio files to the library. Only meaningful with the built-in
 * library — in external mode the music directory is somebody else's, so the
 * caller hides this entirely rather than offering a button that 503s.
 *
 * The rescan the server kicks off is asynchronous, so the new tracks appear via
 * the existing library.updated realtime invalidation rather than being awaited
 * here.
 */
export function UploadTracksButton() {
  const inputRef = useRef<HTMLInputElement>(null)
  const [progress, setProgress] = useState<number | null>(null)
  const pushToast = useToastStore((s) => s.push)
  const qc = useQueryClient()

  async function onFiles(files: FileList | null) {
    if (!files || files.length === 0) return
    setProgress(0)
    try {
      const res = await uploadTracks(Array.from(files), setProgress)
      const n = res.uploaded.length
      const refused = Object.keys(res.rejected ?? {}).length
      pushToast(
        refused > 0
          ? `Added ${n} file${n === 1 ? '' : 's'}, skipped ${refused}`
          : `Added ${n} file${n === 1 ? '' : 's'} — scanning the library`,
        refused > 0 ? 'error' : 'success',
      )
      void qc.invalidateQueries({ queryKey: ['library'] })
    } catch (e) {
      pushToast(e instanceof Error ? e.message : 'Upload failed', 'error')
    } finally {
      setProgress(null)
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  const busy = progress !== null

  return (
    <>
      <input
        ref={inputRef}
        type="file"
        multiple
        accept={UPLOAD_EXTENSIONS.join(',')}
        aria-label="Audio files to upload"
        className="hidden"
        onChange={(e) => void onFiles(e.target.files)}
      />
      <Button
        size="sm"
        variant="secondary"
        aria-label="Upload songs"
        disabled={busy}
        onClick={() => inputRef.current?.click()}
      >
        {busy ? `Uploading… ${Math.round((progress ?? 0) * 100)}%` : '+ Upload songs'}
      </Button>
    </>
  )
}
