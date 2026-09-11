import { useState } from 'react'
import { Chip } from '../components/ui'
import { IntegrationsSection } from '../components/account/IntegrationsSection'
import { AppearanceSection } from '../components/account/AppearanceSection'
import { DownloadsSection } from '../components/account/DownloadsSection'
import { AudioSection } from '../components/account/AudioSection'
import { NotInterestedSection } from '../components/account/NotInterestedSection'
import { useDocumentTitle } from '../lib/useDocumentTitle'

type Tab = 'integrations' | 'downloads' | 'audio' | 'appearance' | 'not-interested'

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

      {/* ── Not interested tab ───────────────────────────────────────────────── */}
      {tab === 'not-interested' && <NotInterestedSection />}
    </div>
  )
}