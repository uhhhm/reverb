
interface ToggleProps {
  checked: boolean
  onChange: (v: boolean) => void
  label: string
}

export function Toggle({ checked, onChange, label }: ToggleProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={[
        'relative inline-flex w-11 h-6 rounded-full transition-colors flex-none',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        checked ? 'bg-accent' : 'bg-raised-hover',
      ].join(' ')}
    >
      <span
        className={[
          'absolute top-[3px] w-[18px] h-[18px] rounded-full transition-transform',
          // On the accent fill the knob takes --on-accent; off the fill it has
          // to invert with the theme instead, or it vanishes into the track.
          checked ? 'bg-on-accent translate-x-[22px]' : 'bg-text-primary translate-x-[3px]',
        ].join(' ')}
      />
    </button>
  )
}
