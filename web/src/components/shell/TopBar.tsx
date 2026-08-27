import { useState, useRef, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { IconButton, Button, Icon, Logo } from '../ui'
import { SearchSuggest } from '../search/SearchSuggest'
import { useUI } from '../../lib/uiStore'
import { useDownloads } from '../../lib/downloadStore'
import { useSearch } from '../../lib/searchStore'
import { useAuthStore } from '../../lib/authStore'

export function TopBar() {
  const navigate = useNavigate()
  const togglePanel = useUI((s) => s.togglePanel)
  const activeCount = useDownloads((s) => s.active().length)
  const query = useSearch((s) => s.query)
  const setQuery = useSearch((s) => s.setQuery)

  const username = useAuthStore((s) => s.me?.username)

  // Typeahead dropdown — typing only updates the shared query; submitting
  // (Enter) navigates to the full /search results page.
  const [suggestOpen, setSuggestOpen] = useState(false)
  const searchRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  function goToResults() {
    navigate('/search')
    setSuggestOpen(false)
    inputRef.current?.blur()
  }

  function submitSearch(e: React.FormEvent) {
    e.preventDefault()
    goToResults()
  }

  // Close the suggestion dropdown on outside click (mousedown, like the avatar menu).
  useEffect(() => {
    if (!suggestOpen) return
    function handler(e: MouseEvent) {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) {
        setSuggestOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [suggestOpen])

  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  // Close menu on outside click
  useEffect(() => {
    if (!menuOpen) return
    function handler(e: MouseEvent) {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [menuOpen])

  return (
    <header className="flex items-center justify-between px-4 h-16 bg-surface">
      {/* Mobile wordmark — desktop shows the history nav + search instead */}
      <div className="md:hidden">
        <Logo iconClassName="h-7 w-auto" textClassName="text-lg" />
      </div>

      {/* Left — history nav (desktop only) */}
      <div className="hidden items-center gap-2 md:flex">
        <IconButton
          name="back"
          label="Back"
          onClick={() => window.history.back()}
        />
        <IconButton
          name="fwd"
          label="Forward"
          onClick={() => window.history.forward()}
        />
      </div>

      {/* Center — home + centered search pill (desktop only; mobile uses the
          Search tab in the bottom nav) */}
      <div className="hidden items-center justify-center gap-2 flex-1 mx-4 min-w-0 md:flex">
        <IconButton
          name="home"
          label="Home"
          onClick={() => navigate('/')}
        />

        <div ref={searchRef} className="relative w-full min-w-0 max-w-md">
          <form
            onSubmit={submitSearch}
            role="search"
            className={[
              'flex items-center gap-3 h-12 px-4 rounded-full bg-input w-full min-w-0',
              'border border-transparent focus-within:border-border-subtle',
              'focus-within:ring-2 focus-within:ring-accent transition-colors',
            ].join(' ')}
          >
            <Icon name="search" className="w-4 h-4 flex-none text-text-secondary" />
            <input
              ref={inputRef}
              type="text"
              aria-label="Search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onFocus={() => setSuggestOpen(true)}
              placeholder="Search your library — or everywhere"
              className="w-full min-w-0 bg-transparent text-sm font-medium text-text-primary placeholder:text-text-secondary outline-none"
            />
          </form>

          {suggestOpen && query.trim() !== '' && (
            <SearchSuggest
              query={query}
              onNavigateAll={goToResults}
              onClose={() => setSuggestOpen(false)}
            />
          )}
        </div>
      </div>

      {/* Right — downloads + avatar */}
      <div className="flex items-center gap-3 flex-none">
        {/* Add-from-link (paste a Spotify/YouTube URL); desktop only */}
        <div className="hidden md:block">
          <Button
            variant="ghost"
            size="sm"
            aria-label="Add from link"
            onClick={() => navigate('/add-from-link')}
          >
            <span className="flex items-center gap-1.5">
              <Icon name="plus" className="w-4 h-4" />
              <span>Add link</span>
            </span>
          </Button>
        </div>

        {/* Stats link (desktop only) */}
        <div className="hidden md:block">
          <Button
            variant="ghost"
            size="sm"
            aria-label="Stats"
            onClick={() => navigate('/stats')}
          >
            <span className="flex items-center gap-1.5">
              <Icon name="chart" className="w-4 h-4" />
              <span>Stats</span>
            </span>
          </Button>
        </div>

        {/* Downloads button with badge (desktop only; mobile uses the bottom nav) */}
        <div className="relative hidden md:block">
          <Button
            variant="ghost"
            size="sm"
            aria-label="Downloads"
            onClick={() => togglePanel('downloads')}
          >
            <span className="flex items-center gap-1.5">
              <Icon name="dl" className="w-4 h-4" />
              <span>Downloads</span>
            </span>
          </Button>
          {activeCount > 0 && (
            <span
              data-testid="downloads-badge"
              className="absolute -top-1 -right-1 min-w-4 h-4 px-1 rounded-full bg-accent text-on-accent text-xs font-extrabold grid place-items-center pointer-events-none"
            >
              {activeCount}
            </span>
          )}
        </div>

        {/* Avatar / account menu */}
        <div ref={menuRef} className="relative">
          <button
            type="button"
            aria-label="Account menu"
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((o) => !o)}
            className={[
              'w-8 h-8 rounded-full bg-accent',
              'flex items-center justify-center text-on-accent font-extrabold text-sm',
              'hover:scale-105 transition-transform',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            ].join(' ')}
          >
            {username?.trim().charAt(0).toUpperCase() || 'R'}
          </button>

          {menuOpen && (
            <div
              role="menu"
              className={[
                'absolute right-0 top-10 w-40 rounded-lg bg-raised shadow-pop border border-border-subtle',
                'py-1 z-50',
              ].join(' ')}
            >
              <button
                role="menuitem"
                type="button"
                onClick={() => { setMenuOpen(false); navigate('/add-from-link') }}
                className={[
                  'w-full text-left px-4 py-2 text-sm text-text-primary md:hidden',
                  'hover:bg-raised-hover transition-colors',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset',
                ].join(' ')}
              >
                Add from link
              </button>
              <button
                role="menuitem"
                type="button"
                onClick={() => { setMenuOpen(false); navigate('/settings') }}
                className={[
                  'w-full text-left px-4 py-2 text-sm text-text-primary',
                  'hover:bg-raised-hover transition-colors',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset',
                ].join(' ')}
              >
                Settings
              </button>
              <button
                role="menuitem"
                type="button"
                onClick={() => { setMenuOpen(false); navigate('/admin') }}
                className={[
                  'w-full text-left px-4 py-2 text-sm text-text-primary',
                  'hover:bg-raised-hover transition-colors',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset',
                ].join(' ')}
              >
                Admin
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  )
}
