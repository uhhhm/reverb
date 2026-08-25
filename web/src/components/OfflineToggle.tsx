import { Toggle } from './ui/Toggle'

interface OfflineToggleProps {
  playlistId: string
  enabled: boolean
  onToggle: (next: boolean) => void
  disabled?: boolean
}

export function OfflineToggle({ enabled, onToggle, disabled }: OfflineToggleProps) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-sm text-text-primary">Keep offline</span>
      <Toggle checked={enabled} label="Keep offline" onChange={onToggle} />
      {disabled && <span className="text-xs text-text-muted">updating…</span>}
    </div>
  )
}
