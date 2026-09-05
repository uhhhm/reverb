import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Button } from './ui/Button'

interface DesktopBackground {
  GetBackgroundSyncEnabled(): Promise<boolean>
  SetBackgroundSyncEnabled(enabled: boolean): Promise<void>
  QuitAndStopSync(): Promise<void>
}

function desktopBackground(): DesktopBackground | undefined {
  const desktopWindow = window as Window & { go?: { main?: { App?: DesktopBackground } } }
  return desktopWindow.go?.main?.App
}

export function BackgroundSyncSettings() {
  const [desktop] = useState(desktopBackground)
  const enabled = useQuery({
    queryKey: ['desktop/background-sync'],
    queryFn: () => desktop!.GetBackgroundSyncEnabled(),
    enabled: !!desktop,
  })
  const save = useMutation({
    mutationFn: (value: boolean) => desktop!.SetBackgroundSyncEnabled(value),
    onSuccess: () => { void enabled.refetch() },
  })
  const quit = useMutation({ mutationFn: () => desktop!.QuitAndStopSync() })
  if (!desktop) return null
  return (
    <section className="rounded-lg border border-border-subtle bg-raised p-4 space-y-3">
      <h2 className="font-semibold">Background sync</h2>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled.data ?? false}
          disabled={enabled.isPending || enabled.isError || save.isPending}
          onChange={(e) => save.mutate(e.target.checked)} />
        Keep syncing after closing the window
      </label>
      <p className="text-sm text-text-secondary">
        A background process keeps your paired devices in sync without keeping the window in memory.
        Both computers must be awake and online. Open Reverb again to return to the app.
        Background sync starts after you use Reverb; it does not start at login.
      </p>
      <Button size="sm" onClick={() => quit.mutate()} disabled={quit.isPending}>Quit Reverb and stop sync</Button>
      {(enabled.error || save.error || quit.error) && (
        <p role="alert" className="text-sm text-error">{(enabled.error || save.error || quit.error)?.message}</p>
      )}
    </section>
  )
}
