/**
 * Checkbox renders its own box and tick instead of relying on the native
 * control. Chromium draws the native checkmark for a 13px box and scales it when
 * width/height are overridden, which leaves the tick visibly off-centre at 16px.
 * The input stays in the DOM (opacity-0, layered over the box) so focus,
 * keyboard, labels and testing-library's getByRole all behave normally.
 */
interface CheckboxProps {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  id?: string
}

export function Checkbox({ checked, onChange, label, id }: CheckboxProps) {
  return (
    <span className="relative inline-flex h-4 w-4 flex-none items-center justify-center">
      <input
        id={id}
        type="checkbox"
        aria-label={label}
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="peer absolute inset-0 z-10 m-0 h-full w-full cursor-pointer opacity-0"
      />
      <span
        aria-hidden="true"
        className={[
          'pointer-events-none grid h-4 w-4 place-items-center rounded-[3px] border transition-colors',
          'peer-focus-visible:ring-2 peer-focus-visible:ring-accent peer-focus-visible:ring-offset-1 peer-focus-visible:ring-offset-surface',
          checked ? 'border-accent bg-accent' : 'border-border-subtle bg-input',
        ].join(' ')}
      >
        <svg viewBox="0 0 16 16" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth={2.5} strokeLinecap="round" strokeLinejoin="round">
          <path d="M3.5 8.5l3 3 6-6" className={checked ? 'text-on-accent' : 'text-transparent'} stroke="currentColor" />
        </svg>
      </span>
    </span>
  )
}
