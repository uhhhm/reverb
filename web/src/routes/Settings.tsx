import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Chip } from '../components/ui'
import { IntegrationsSection } from '../components/account/IntegrationsSection'
import { AppearanceSection } from '../components/account/AppearanceSection'
import { DownloadsSection } from '../components/account/DownloadsSection'
import { AudioSection } from '../components/account/AudioSection'
import { NotInterestedSection } from '../components/account/NotInterestedSection'
import { RecommendationsSection } from '../components/account/RecommendationsSection'
import { useDocumentTitle } from '../lib/useDocumentTitle'
import { fetchVersionInfo } from '../lib/updateApi'

type Tab = 'integrations' | 'downloads' | 'audio' | 'appearance' | 'recommendations' | 'not-interested'

export default function Settings() {
  useDocumentTitle('Settings')
  const [tab, setTab] = useState<Tab>('integrations')

  return (
    <div className="max-w-4xl space-y-6 pb-8">
      {/* Header */}
      <h1 className="text-3xl font-black tracking-tight text-text-primary">Settings</h1>

      {/* Tab bar */}
      <div role="tablist" aria-label="Settings sections" className="flex gap-2 border-b border-border-subtle pb-3 flex-wrap">
        <Chip selected={tab === 'integrations'} onClick={() => setTab('integrations')}>
          Integrations
        </Chip>
        <Chip selected={tab === 'downloads'} onClick={() => setTab('downloads')}>
          Downloads
        </Chip>
        <Chip selected={tab === 'audio'} onClick={() => setTab('audio')}>
          Audio
        </Chip>
        <Chip selected={tab === 'appearance'} onClick={() => setTab('appearance')}>
          Appearance
        </Chip>
        <Chip selected={tab === 'recommendations'} onClick={() => setTab('recommendations')}>
          Recommendations
        </Chip>
        <Chip selected={tab === 'not-interested'} onClick={() => setTab('not-interested')}>
          Not interested
        </Chip>
      </div>

      {/* ── Integrations tab ─────────────────────────────────────────────────── */}
      {tab === 'integrations' && <IntegrationsSection />}

      {/* ── Downloads tab ────────────────────────────────────────────────────── */}
      {tab === 'downloads' && <DownloadsSection />}

      {/* ── Audio tab ────────────────────────────────────────────────────────── */}
      {tab === 'audio' && <AudioSection />}

      {/* ── Appearance tab ───────────────────────────────────────────────────── */}
      {tab === 'appearance' && <AppearanceSection />}

      {/* ── Recommendations tab ──────────────────────────────────────────────── */}
      {tab === 'recommendations' && <RecommendationsSection />}

      {/* ── Not interested tab ───────────────────────────────────────────────── */}
      {tab === 'not-interested' && <NotInterestedSection />}

      <AppVersion />
    </div>
  )
}

// displayVersion writes a release number as a tag ("0.1.1" → "v0.1.1") and
// leaves anything else, such as a "dev" build, as the server reported it.
function displayVersion(version: string): string {
  return /^\d/.test(version) ? `v${version}` : version
}

function AppVersion() {
  const { data } = useQuery({ queryKey: ['version'], queryFn: fetchVersionInfo, staleTime: Infinity })
  if (!data?.version) return null
  return (
    <p className="border-t border-border-subtle pt-4 text-xs text-text-muted">
      Reverb {displayVersion(data.version)}
    </p>
  )
}
