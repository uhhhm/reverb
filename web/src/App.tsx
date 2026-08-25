import { useEffect, lazy } from 'react'
import { Navigate, Route, Routes, useParams } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AppShell } from './components/AppShell'
import { ApiError } from './lib/api'
import { useAuthStore } from './lib/authStore'
// Lazy: the heavy routes. Splitting these out of the main bundle keeps the
// initial payload small and clears the build size warning.
const Home = lazy(() => import('./routes/Home'))
const Search = lazy(() => import('./routes/Search'))
const Library = lazy(() => import('./routes/Library'))
const Settings = lazy(() => import('./routes/Settings'))
const Album = lazy(() => import('./routes/Album'))
const Artist = lazy(() => import('./routes/Artist'))
const Admin = lazy(() => import('./routes/Admin'))
const Downloads = lazy(() => import('./routes/Downloads'))
const SyncedPlaylist = lazy(() => import('./routes/SyncedPlaylist'))
const Stats = lazy(() => import('./routes/Stats'))
const ExternalPlaylist = lazy(() => import('./routes/ExternalPlaylist'))
const Collection = lazy(() => import('./routes/Collection'))
const Pairing = lazy(() => import('./routes/Pairing'))
const OfflineSet = lazy(() => import('./routes/OfflineSet'))
const AddFromLink = lazy(() => import('./routes/AddFromLink'))

/** Redirect bare `/album/:id` or `/artist/:id` URLs to the source-qualified form
 *  `/album/library/:id` / `/artist/library/:id`. These old URLs may exist in
 *  bookmarks or nav links written before the source segment was introduced. */
function RedirectToLibrary({ kind }: { kind: 'album' | 'artist' }) {
  const { id = '' } = useParams()
  return <Navigate to={`/${kind}/library/${id}`} replace />
}

/** Redirect legacy `/synced-playlist/:id` URLs to the canonical `/playlist/:id` form. */
function RedirectToPlaylist() {
  const { id = '' } = useParams()
  return <Navigate to={`/playlist/${id}`} replace />
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // Don't hammer endpoints that fail deterministically: 4xx and the
      // library's 503 "no library configured" won't change on retry. Other
      // 5xx may be transient, so retry those once.
      retry: (failureCount, error) => {
        if (error instanceof ApiError && (error.status === 503 || (error.status >= 400 && error.status < 500))) {
          return false
        }
        return failureCount < 1
      },
    },
  },
})

function Routed() {
  const refresh = useAuthStore((st) => st.refresh)

  // Boot-hydrate the auth store so `can()` is populated app-wide. Reverb is
  // single-user with no login, so there is no gate: the shell always renders.
  useEffect(() => {
    void refresh()
  }, [refresh])

  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route path="/" element={<Home />} />
        <Route path="/search" element={<Search />} />
        <Route path="/library" element={<Library />} />
        <Route path="/collection" element={<Collection />} />
        <Route path="/album/:source/:id" element={<Album />} />
        <Route path="/album/:id" element={<RedirectToLibrary kind="album" />} />
        <Route path="/artist/:source/:id" element={<Artist />} />
        <Route path="/artist/:id" element={<RedirectToLibrary kind="artist" />} />
        <Route path="/playlist/:id" element={<SyncedPlaylist />} />
        <Route path="/playlist/:source/:id" element={<ExternalPlaylist />} />
        <Route path="/synced-playlist/:id" element={<RedirectToPlaylist />} />
        <Route path="/stats" element={<Stats />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/admin" element={<Admin />} />
        <Route path="/downloads" element={<Downloads />} />
        <Route path="/pairing" element={<Pairing />} />
        <Route path="/offline-set" element={<OfflineSet />} />
        <Route path="/add-from-link" element={<AddFromLink />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Routed />
    </QueryClientProvider>
  )
}