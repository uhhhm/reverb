import { useState } from 'react'
import type { Track } from '../lib/types'
import { useSettings } from '../lib/settingsApi'
import { useUpgradeDownload } from '../lib/upgradeApi'
import { useToastStore } from '../lib/toastStore'
import { DEFAULT_AUDIO_QUALITY, isUpgrade, qualityForBitrate, qualityLabel } from '../lib/audioQuality'
import { Icon } from './ui/Icon'

/**
 * Row action offering to re-download a track at the configured quality tier.
 *
 * Only shown for owned tracks whose current bitrate sits below that tier — there
 * is nothing to gain otherwise, and an unowned search result has no file to
 * replace.
 */
export function UpgradeQualityButton({ track }: { track: Track }) {
  const { data: settings } = useSettings()
  const upgrade = useUpgradeDownload()
  const pushToast = useToastStore((s) => s.push)
  const [done, setDone] = useState(false)

  const target = settings?.downloadQuality ?? DEFAULT_AUDIO_QUALITY
  const current = qualityForBitrate(track.bitRate)

  if (!track.id || !current || !isUpgrade(current, target) || done) return null

  return (
    <button
      type="button"
      aria-label={`Upgrade quality to ${qualityLabel(target)}`}
      title={`${track.bitRate} kbps — re-download at ${qualityLabel(target)}`}
      disabled={upgrade.isPending}
      onClick={(e) => {
        e.stopPropagation()
        upgrade.mutate(
          {
            artist: track.artist,
            title: track.title,
            album: track.album,
            quality: target,
            currentQuality: current,
          },
          {
            onSuccess: () => {
              setDone(true)
              pushToast(`Re-downloading “${track.title}” at ${qualityLabel(target)}`, 'success')
            },
            onError: () => pushToast('Could not queue the upgrade', 'error'),
          },
        )
      }}
      onDoubleClick={(e) => e.stopPropagation()}
      className={[
        'inline-grid h-7 w-7 place-items-center rounded-md',
        'text-text-muted hover:text-text-primary',
        'opacity-0 transition-opacity duration-150 group-hover:opacity-100',
        'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        'disabled:opacity-50',
      ].join(' ')}
    >
      <Icon name="up" className="h-3.5 w-3.5" />
    </button>
  )
}
