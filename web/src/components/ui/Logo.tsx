import { useState } from 'react'

interface LogoProps {
  /** Height utility for the icon mark, e.g. "h-8 w-auto". */
  iconClassName?: string
  /** Text-size utility for the wordmark, e.g. "text-2xl". */
  textClassName?: string
}

/**
 * Brand lockup: the Reverb mark + the "Reverb." wordmark. Both icon variants
 * are rendered and CSS picks one (`rvb-logo-*` in index.css), because the mark
 * is a fixed-colour asset: `/Reverb-Light.svg` has white strokes that vanish on
 * the light theme, `/logo.svg` has dark strokes that vanish on the dark ones.
 * If the icon asset is missing it shows the wordmark alone — never a broken image.
 */
export function Logo({ iconClassName = 'h-8 w-auto', textClassName = 'text-2xl' }: LogoProps) {
  const [iconOk, setIconOk] = useState(true)

  return (
    <span className="inline-flex select-none items-center gap-2">
      {iconOk && (
        <>
          <img
            src="/Reverb-Light.svg"
            alt=""
            aria-hidden="true"
            className={`rvb-logo-on-dark ${iconClassName}`}
            onError={() => setIconOk(false)}
          />
          <img
            src="/logo.svg"
            alt=""
            aria-hidden="true"
            className={`rvb-logo-on-light ${iconClassName}`}
          />
        </>
      )}
      <span className={`font-bold tracking-tight text-text-primary ${textClassName}`}>
        Reverb<span className="text-accent">.</span>
      </span>
    </span>
  )
}
