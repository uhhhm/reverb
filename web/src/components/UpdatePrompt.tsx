import { useEffect } from 'react'
import { useUpdateStore } from '../lib/updateStore'
import { installUpdate, dismissUpdate } from '../lib/updateApi'
import { ApiError } from '../lib/api'

// UpdatePrompt offers a restart once a new version has been downloaded and
// verified in the background. It never installs on its own: the update sits
// staged until the user chooses a moment, and "Later" keeps the download.
export function UpdatePrompt() {
  const state = useUpdateStore((s) => s.state)
  const installing = useUpdateStore((s) => s.installing)
  const installError = useUpdateStore((s) => s.installError)
  const shouldPrompt = useUpdateStore((s) => s.shouldPrompt())
  const refresh = useUpdateStore((s) => s.refresh)

  useEffect(() => {
    void refresh()
  }, [refresh])

  if (!shouldPrompt) return null

  async function onRestart() {
    const store = useUpdateStore.getState()
    store.setInstallError('')
    store.setInstalling(true)
    try {
      await installUpdate()
    } catch (err) {
      if (err instanceof ApiError) {
        // The server answered, so it is still running and the install did not
        // happen: say why instead of pretending to restart.
        const reason = typeof err.body?.error === 'string' ? err.body.error : ''
        store.setInstalling(false)
        store.setInstallError(reason || `Reverb couldn't install the update (HTTP ${err.status}).`)
        void store.refresh()
        return
      }
      // The connection dropping is the expected outcome of a successful
      // restart, so it is not reported as a failure. If the app is still here
      // a moment later, the prompt returns.
      window.setTimeout(() => {
        useUpdateStore.getState().setInstalling(false)
        void useUpdateStore.getState().refresh()
      }, 5000)
    }
  }

  function onLater() {
    useUpdateStore.getState().dismiss()
    void dismissUpdate().catch(() => {})
  }

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="update-prompt"
      className="fixed bottom-24 right-6 z-50 w-80 rounded-lg border border-border-subtle bg-raised p-4 shadow-pop md:bottom-28"
    >
      <p className="text-sm font-semibold text-text-primary">
        Reverb {state.staged} is ready
      </p>
      <p className="mt-1 text-xs text-text-muted">
        {installing
          ? 'Restarting to finish the update…'
          : 'It has been downloaded. Restart whenever suits you — nothing changes until you do.'}
      </p>
      {installError && !installing ? (
        <p role="alert" className="mt-2 break-words text-xs text-error">
          Update failed: {installError}
        </p>
      ) : null}
      <div className="mt-3 flex items-center gap-2">
        <button
          type="button"
          onClick={() => void onRestart()}
          disabled={installing}
          className="inline-flex items-center justify-center rounded-full bg-accent px-3 py-1.5 text-sm font-semibold text-on-accent hover:opacity-90 disabled:opacity-60"
        >
          {installing ? 'Restarting…' : 'Restart now'}
        </button>
        <button
          type="button"
          onClick={onLater}
          disabled={installing}
          className="inline-flex items-center justify-center rounded-full border border-border-subtle px-3 py-1.5 text-sm font-semibold text-text-primary hover:bg-surface disabled:opacity-60"
        >
          Later
        </button>
      </div>
    </div>
  )
}
