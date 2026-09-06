import type { ReactNode } from 'react'
import { usePlayer } from '../lib/playerStore'
import { Icon } from './ui'

import type { PlaylistSort } from '../lib/playlistOrder'

export function PlaylistControls({ name, disabled, playing, onPlay, query, onQuery, sort, onSort, children }: {
  name: string; disabled: boolean; playing: boolean; onPlay: () => void
  query: string; onQuery: (query: string) => void; sort: PlaylistSort; onSort: (sort: PlaylistSort) => void
  children?: ReactNode
}) {
  const pause = usePlayer((s) => s.pause)
  const shuffle = usePlayer((s) => s.shuffle)
  const toggleShuffle = usePlayer((s) => s.toggleShuffle)
  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-4 py-1" aria-label="Playlist controls">
      <button type="button" aria-label={`${playing ? 'Pause' : 'Play'} ${name}`} disabled={disabled}
        onClick={playing ? pause : onPlay}
        className="grid h-14 w-14 shrink-0 place-items-center rounded-full bg-accent text-on-accent transition-transform hover:scale-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-4 focus-visible:ring-offset-surface disabled:opacity-40 disabled:hover:scale-100">
        <Icon name={playing ? 'pause' : 'play'} className="h-6 w-6" />
      </button>
      <button type="button" aria-label="Shuffle" aria-pressed={shuffle} onClick={toggleShuffle} title="Shuffle"
        className={`playlist-tool ${shuffle ? 'text-accent' : 'text-text-secondary'}`}>
        <Icon name="shuffle" className="h-6 w-6" />
      </button>
      {children}
      <div className="ml-auto flex items-center gap-3 max-sm:w-full">
        <label className="flex items-center gap-2 rounded-md bg-white/5 px-3 py-2 text-text-secondary focus-within:ring-1 focus-within:ring-text-muted">
          <Icon name="search" className="h-4 w-4 shrink-0" />
          <input aria-label="Search in playlist" placeholder="Search in playlist" value={query} onChange={(e) => onQuery(e.target.value)}
            className="w-32 bg-transparent text-sm text-text-primary outline-none placeholder:text-text-muted sm:w-36" />
          {query && <button type="button" aria-label="Clear playlist search" onClick={() => onQuery('')}><Icon name="x" className="h-4 w-4" /></button>}
        </label>
        <label className="flex items-center gap-2 text-text-secondary">
          <select aria-label="Sort playlist" value={sort} onChange={(e) => onSort(e.target.value as PlaylistSort)} className="min-w-0 cursor-pointer appearance-none rounded bg-transparent py-2 pl-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-accent">
            <option value="custom">Custom order</option><option value="title">Title</option><option value="artist">Artist</option><option value="album">Album</option><option value="duration">Duration</option>
          </select>
          <Icon name="sort" className="h-4 w-4" />
        </label>
      </div>
    </div>
  )
}

export function PlaylistColumns() {
  return <div className="playlist-grid border-b border-border-subtle py-2 text-sm text-text-secondary" aria-hidden="true">
    <span className="text-center">#</span><span className="col-span-2">Title</span>
    <span className="playlist-album">Album</span><span />
    <span className="flex justify-end" title="Duration"><svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.5"><circle cx="12" cy="12" r="9" /><path d="M12 6v6l4 2" /></svg></span><span />
  </div>
}
