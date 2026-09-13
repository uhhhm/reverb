import { useState } from 'react'
import { Toggle } from '../ui'
import { useRecommendationSettings, useUpdateRecommendationSettings } from '../../lib/recommendationsApi'

/** Settings panel — how adventurous recommendations are, and whether they look anything up online. */
export function RecommendationsSection() {
  const { data } = useRecommendationSettings()
  const update = useUpdateRecommendationSettings()
  // The slider moves freely; the value is saved (and replicated) when it is let go.
  const [draft, setDraft] = useState<number | null>(null)
  const adventurousness = draft ?? data?.adventurousness ?? 50

  function commit() {
    if (draft === null) return
    if (draft === data?.adventurousness) {
      setDraft(null)
      return
    }
    update.mutate({ adventurousness: draft }, { onSettled: () => setDraft(null) })
  }

  return (
    <div className="space-y-0 divide-y divide-border-subtle">
      <div className="py-5">
        <div className="text-sm font-bold text-text-primary">Adventurousness</div>
        <div className="mt-0.5 text-xs text-text-secondary">
          How much of what Reverb recommends is new to you, rather than music you already own or have played. The
          middle keeps each place&apos;s usual balance: Radio is about half new. Applies on every paired device.
        </div>
        <div className="mt-3 flex items-center gap-3">
          <span className="text-xs text-text-muted">Familiar</span>
          <input
            type="range"
            min={0}
            max={100}
            step={5}
            value={adventurousness}
            aria-label="Adventurousness"
            aria-valuetext={`${adventurousness}% towards new music`}
            className="flex-1 accent-accent"
            onChange={(e) => setDraft(Number(e.target.value))}
            onPointerUp={commit}
            onKeyUp={commit}
            onBlur={commit}
          />
          <span className="text-xs text-text-muted">New</span>
        </div>
      </div>

      <div className="flex items-center gap-5 py-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold text-text-primary">Online recommendations</div>
          <div className="mt-0.5 text-xs text-text-secondary">
            When on, Reverb sends the artist and title of each track or artist a recommendation starts from to
            Last.fm, ListenBrainz and Deezer to find similar music, and searches your music sources for playable
            copies. Nothing else from your listening history is sent. When off, nothing is sent, and recommendations
            that need online data are hidden. Applies on every paired device.
          </div>
        </div>
        <div className="flex-none">
          <Toggle
            checked={data?.onlineRecommendations ?? true}
            label="Online recommendations"
            onChange={(v) => update.mutate({ onlineRecommendations: v })}
          />
        </div>
      </div>
    </div>
  )
}
