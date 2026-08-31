import { useSettings } from './settingsApi'
import { findRefetchable, useRefetchable, useUpgradeDownload } from './upgradeApi'
import { useToastStore } from './toastStore'
import { DEFAULT_AUDIO_QUALITY, qualityLabel } from './audioQuality'
import type { AudioQuality } from './audioQuality'
import type { Track } from './types'

/**
 * Whether a track can be re-fetched at a different tier, and the actions that
 * do it. The tier may be higher or lower — a deliberate downgrade to save space
 * is as valid as an upgrade.
 *
 * Availability comes from the server's re-fetchable list rather than from the
 * track's bitrate. A bitrate below the configured tier does not mean a better
 * file exists — the sources behind both downloaders serve ~130-160 kbps, so most
 * low-bitrate files are already the best that provider has. The server list is
 * built from download history, so it only contains tracks Reverb fetched itself
 * and still knows a source for, which is also the only case where re-fetching
 * lands on the same recording.
 */
export interface TrackUpgrade {
  available: boolean
  /** The track's standing target tier (global setting; per-track override is applied server-side). */
  target: AudioQuality
  /** Tier the existing file was fetched at, when known. */
  current?: AudioQuality
  isPending: boolean
  /** Re-fetch at the standing target tier. */
  run: () => void
  /** Re-fetch at an explicit tier, optionally making it this track's standing quality. */
  runAt: (quality: AudioQuality, opts?: { setOverride?: boolean }) => void
}

export function useTrackUpgrade(track: Track): TrackUpgrade {
  const { data: settings } = useSettings()
  const target = settings?.downloadQuality ?? DEFAULT_AUDIO_QUALITY
  const { data: upgradable } = useRefetchable()
  const upgrade = useUpgradeDownload()
  const pushToast = useToastStore((s) => s.push)

  const entry = findRefetchable(upgradable, track)

  function submit(quality: AudioQuality, setOverride?: boolean) {
    if (!entry) return
    upgrade.mutate(
      {
        source: entry.source,
        externalId: entry.externalId,
        libraryTrackId: entry.libraryTrackId ?? track.id,
        artist: track.artist,
        title: track.title,
        album: track.album,
        quality,
        currentQuality: entry.quality,
        setOverride,
      },
      {
        onSuccess: () =>
          pushToast(`Re-downloading “${track.title}” at ${qualityLabel(quality)}`, 'success'),
        onError: () => pushToast('Could not queue the re-download', 'error'),
      },
    )
  }

  return {
    available: !!entry,
    target,
    current: entry?.quality,
    isPending: upgrade.isPending,
    run: () => submit(target),
    runAt: (quality, opts) => submit(quality, opts?.setOverride),
  }
}
