import { useSettings } from './settingsApi'
import { useUpgradable, useUpgradeDownload } from './upgradeApi'
import type { UpgradableTrack } from './upgradeApi'
import { useToastStore } from './toastStore'
import { DEFAULT_AUDIO_QUALITY, qualityLabel } from './audioQuality'
import type { AudioQuality } from './audioQuality'
import type { Track } from './types'

/**
 * Whether a track can be re-fetched at a higher tier, and the action that does it.
 *
 * Availability comes from the server's upgradable list rather than from the
 * track's bitrate. A bitrate below the configured tier does not mean a better
 * file exists — the sources behind both downloaders serve ~130-160 kbps, so most
 * low-bitrate files are already the best that provider has. The server list is
 * built from download history, so it only contains tracks Reverb fetched itself
 * and still knows a source for, which is also the only case where re-fetching
 * lands on the same recording.
 */
export interface TrackUpgrade {
  available: boolean
  target: AudioQuality
  /** Tier the existing file was fetched at, when known. */
  current?: AudioQuality
  isPending: boolean
  run: () => void
}

export function useTrackUpgrade(track: Track): TrackUpgrade {
  const { data: settings } = useSettings()
  const target = settings?.downloadQuality ?? DEFAULT_AUDIO_QUALITY
  const { data: upgradable } = useUpgradable(target)
  const upgrade = useUpgradeDownload()
  const pushToast = useToastStore((s) => s.push)

  let entry: UpgradableTrack | undefined
  if (track.id && Array.isArray(upgradable)) {
    entry = upgradable.find((u) => u.libraryTrackId != null && u.libraryTrackId !== '' && u.libraryTrackId === track.id)
    if (!entry) {
      const wantArtist = track.artist.trim().toLowerCase()
      const wantTitle = track.title.trim().toLowerCase()
      entry = upgradable.find(
        (u) => u.artist.trim().toLowerCase() === wantArtist && u.title.trim().toLowerCase() === wantTitle,
      )
    }
  }

  return {
    available: !!entry,
    target,
    current: entry?.quality,
    isPending: upgrade.isPending,
    run: () => {
      if (!entry) return
      upgrade.mutate(
        {
          source: entry.source,
          externalId: entry.externalId,
          libraryTrackId: entry.libraryTrackId ?? track.id,
          artist: track.artist,
          title: track.title,
          album: track.album,
          quality: target,
          currentQuality: entry.quality,
        },
        {
          onSuccess: () =>
            pushToast(`Re-downloading “${track.title}” at ${qualityLabel(target)}`, 'success'),
          onError: () => pushToast('Could not queue the upgrade', 'error'),
        },
      )
    },
  }
}
