import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Checkbox, Modal } from './ui'
import { compileRule, previewChanges, EMPTY_RULE, type ReplaceRule } from '../lib/findReplace'
import {
  BATCH_LIMIT,
  batchRename,
  type BatchRenameRequest,
  type EntityRenameItem,
  type TrackRenameItem,
} from '../lib/libraryEditApi'
import type { Album, Artist, Track } from '../lib/types'

/** What the dialog is renaming. Each kind has its own set of editable fields. */
export type RenameSubject =
  | { kind: 'tracks'; items: Track[] }
  | { kind: 'albums'; items: Album[] }
  | { kind: 'artists'; items: Artist[] }

interface BatchRenameDialogProps {
  subject: RenameSubject | null
  onClose: () => void
  /** Called after a successful apply, so the caller can drop its selection. */
  onApplied?: () => void
}

type FieldKey = 'title' | 'artist' | 'album' | 'name'

const TRACK_FIELDS: { key: FieldKey; label: string; get: (t: Track) => string }[] = [
  { key: 'title', label: 'Title', get: (t) => t.title },
  { key: 'artist', label: 'Artist', get: (t) => t.artist },
  { key: 'album', label: 'Album', get: (t) => t.album ?? '' },
]

/**
 * Find-and-replace across a selection of tracks, albums, or artists.
 *
 * The rule runs here so the user sees every resulting name before anything is
 * written; the request carries those literal names rather than the pattern.
 * Nothing touches the files — Reverb stores the result as a display override.
 */
export function BatchRenameDialog({ subject, onClose, onApplied }: BatchRenameDialogProps) {
  const qc = useQueryClient()
  const [rule, setRule] = useState<ReplaceRule>(EMPTY_RULE)
  const [fields, setFields] = useState<FieldKey[]>(['title'])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const compiled = useMemo(() => compileRule(rule), [rule])

  // Albums and artists have exactly one editable name, so the field choice only
  // means anything for tracks.
  const activeFields = useMemo(() => {
    if (!subject) return []
    if (subject.kind === 'tracks') return TRACK_FIELDS.filter((f) => fields.includes(f.key))
    if (subject.kind === 'albums') {
      return [{ key: 'name' as FieldKey, label: 'Name', get: (a: Album) => a.name }]
    }
    return [{ key: 'name' as FieldKey, label: 'Name', get: (a: Artist) => a.name }]
  }, [subject, fields])

  const changes = useMemo(() => {
    if (!subject) return []
    // The item type varies by subject, and the field getters were chosen to
    // match it just above.
    const specs = activeFields.map((f) => ({
      name: f.label,
      get: f.get as unknown as (i: { id: string }) => string,
    }))
    return previewChanges(subject.items as { id: string }[], specs, compiled)
  }, [subject, activeFields, compiled])

  if (!subject) return null

  const overLimit = changes.length > BATCH_LIMIT

  async function handleApply() {
    if (busy || !subject || changes.length === 0 || overLimit) return
    setBusy(true)
    setError(null)
    try {
      await batchRename(buildRequest(subject, changes))
      // Every list that can show these names re-reads from the library.
      await qc.invalidateQueries({ queryKey: ['library'] })
      await qc.invalidateQueries({ queryKey: ['album-detail'] })
      await qc.invalidateQueries({ queryKey: ['synced-playlist'] })
      onApplied?.()
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't apply these renames")
      setBusy(false)
    }
  }

  const noun = subject.kind === 'tracks' ? 'track' : subject.kind === 'albums' ? 'album' : 'artist'

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      testId="batch-rename-dialog"
      title={`Rename ${subject.items.length} ${noun}${subject.items.length === 1 ? '' : 's'}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={() => void handleApply()}
            disabled={busy || changes.length === 0 || overLimit}
          >
            {busy ? 'Applying…' : `Apply ${changes.length} change${changes.length === 1 ? '' : 's'}`}
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <label htmlFor="batch-find" className="block text-sm font-semibold text-text-primary">
            Find
          </label>
          <input
            id="batch-find"
            value={rule.find}
            onChange={(e) => setRule({ ...rule, find: e.target.value })}
            placeholder="(Remastered)"
            className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          />
        </div>
        <div className="space-y-1.5">
          <label htmlFor="batch-replace" className="block text-sm font-semibold text-text-primary">
            Replace with
          </label>
          <input
            id="batch-replace"
            value={rule.replace}
            onChange={(e) => setRule({ ...rule, replace: e.target.value })}
            placeholder="(leave blank to delete)"
            className="w-full rounded-lg border border-border-subtle bg-input px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          />
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-text-primary">
        <label className="flex items-center gap-2">
          <Checkbox
            checked={rule.matchCase}
            onChange={(v) => setRule({ ...rule, matchCase: v })}
            label="Match case"
          />
          Match case
        </label>
        <label className="flex items-center gap-2">
          <Checkbox
            checked={rule.useRegex}
            onChange={(v) => setRule({ ...rule, useRegex: v })}
            label="Regular expression"
          />
          Regular expression
        </label>
      </div>

      {subject.kind === 'tracks' && (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-text-primary">
          <span className="text-text-muted">Apply to:</span>
          {TRACK_FIELDS.map((f) => (
            <label key={f.key} className="flex items-center gap-2">
              <Checkbox
                checked={fields.includes(f.key)}
                onChange={(v) =>
                  setFields((prev) => (v ? [...prev, f.key] : prev.filter((k) => k !== f.key)))
                }
                label={f.label}
              />
              {f.label}
            </label>
          ))}
        </div>
      )}

      {!compiled.ok && (
        <p role="alert" className="text-sm text-error">
          {compiled.error}
        </p>
      )}

      <div className="rounded-lg border border-border-subtle">
        <div className="border-b border-border-subtle px-3 py-2 text-xs font-semibold text-text-muted">
          Preview — {changes.length} change{changes.length === 1 ? '' : 's'}
        </div>
        {changes.length === 0 ? (
          <p className="px-3 py-6 text-center text-sm text-text-muted">
            {rule.find === '' ? 'Type something to find.' : 'Nothing matches.'}
          </p>
        ) : (
          <ul className="max-h-64 divide-y divide-border-subtle overflow-y-auto text-sm">
            {changes.slice(0, 200).map((c, i) => (
              <li key={`${c.item.id}-${c.field}-${i}`} className="flex gap-2 px-3 py-2">
                <span className="w-14 flex-none text-xs uppercase text-text-muted">{c.field}</span>
                <span className="min-w-0 flex-1 truncate text-text-muted line-through">{c.before}</span>
                <span className="min-w-0 flex-1 truncate text-text-primary">{c.after}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {overLimit && (
        <p role="alert" className="text-sm text-error">
          That is more than {BATCH_LIMIT} changes. Narrow the selection and apply it in parts.
        </p>
      )}

      <p className="text-xs text-text-muted">
        This changes names in Reverb only — your files and Navidrome are left untouched.
      </p>

      {error && (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      )}
    </Modal>
  )
}

/**
 * Collapses the per-field changes into one request. A track with two changed
 * fields is one item carrying both, and nothing else: a field the user did not
 * change is left out so the server keeps it. Sending it as it stands here would
 * pin the name currently on screen — which may be an album or artist rename
 * cascading down — as a per-track override, and per-track wins, so a later edit
 * to that album would no longer reach the track.
 */
function buildRequest(
  subject: RenameSubject,
  changes: { item: { id: string }; field: string; after: string }[],
): BatchRenameRequest {
  if (subject.kind === 'tracks') {
    const byId = new Map<string, TrackRenameItem>()
    const source = new Map(subject.items.map((t) => [t.id, t]))
    for (const c of changes) {
      const t = source.get(c.item.id)
      if (!t) continue
      const entry = byId.get(t.id) ?? { id: t.id }
      if (c.field === 'Title') entry.title = c.after
      if (c.field === 'Artist') entry.artist = c.after
      if (c.field === 'Album') entry.album = c.after
      byId.set(t.id, entry)
    }
    return { tracks: [...byId.values()] }
  }
  const items: EntityRenameItem[] = changes.map((c) => ({ id: c.item.id, name: c.after }))
  return subject.kind === 'albums' ? { albums: items } : { artists: items }
}
