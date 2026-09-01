import { api } from './api'

// Fallback only. The repository is server-configured (REVERB_UPDATE_REPO /
// --update-repo) and reported by /version as updateRepo.
const DEFAULT_REPO = 'uhhhm/reverb'

// isNewer reports whether latest is newer than current. Mirrors
// desktop/updater semver logic: strips leading "v", numeric compare.
export function isNewer(current: string, latest: string): boolean {
  if (!latest || !current) return false
  if (latest === current) return false
  const cur = current.trim()
  const lat = latest.trim()
  if (cur === 'dev' || lat === 'dev') return false
  const curNorm = cur.startsWith('v') ? cur.slice(1) : cur
  const latNorm = lat.startsWith('v') ? lat.slice(1) : lat
  if (curNorm === latNorm) return false

  const curCore = curNorm.split('-')[0].split('+')[0]
  const latCore = latNorm.split('-')[0].split('+')[0]
  const curParts = curCore.split('.')
  const latParts = latCore.split('.')
  const maxLen = Math.max(curParts.length, latParts.length)
  for (let i = 0; i < maxLen; i++) {
    const curN = i < curParts.length ? parseInt(curParts[i], 10) || 0 : 0
    const latN = i < latParts.length ? parseInt(latParts[i], 10) || 0 : 0
    if (latN > curN) return true
    if (latN < curN) return false
  }
  const curHasPre = (current.startsWith('v') ? current.slice(1) : current).includes('-')
  const latHasPre = (latest.startsWith('v') ? latest.slice(1) : latest).includes('-')
  if (curHasPre && !latHasPre) return true
  if (!curHasPre && latHasPre) return false
  return lat > cur
}

export interface VersionInfo {
  version: string
  // GitHub owner/name to poll for releases; '' when updates are disabled.
  updateRepo: string
}

export async function fetchVersionInfo(): Promise<VersionInfo> {
  const data = await api.get<{ version: string; updateRepo?: string }>('/version')
  return { version: data.version, updateRepo: data.updateRepo ?? DEFAULT_REPO }
}

export async function fetchVersion(): Promise<string> {
  return (await fetchVersionInfo()).version
}

// UpdateState mirrors desktop/updater.State. The backend does the polling,
// downloading and version comparison; the UI only reflects it and decides when
// to ask. Nothing is ever installed without the user pressing Restart.
export interface UpdateState {
  currentVersion: string
  repo: string
  checking: boolean
  // available is the newer tag on offer, '' when this build is current.
  available: string
  notes: string
  downloading: boolean
  // progress runs 0..1 while downloading.
  progress: number
  // staged is the tag already downloaded and waiting for a restart.
  staged: string
  error: string
  lastCheck?: string
}

export const EMPTY_UPDATE_STATE: UpdateState = {
  currentVersion: '',
  repo: '',
  checking: false,
  available: '',
  notes: '',
  downloading: false,
  progress: 0,
  staged: '',
  error: '',
}

export async function fetchUpdateState(): Promise<UpdateState> {
  return { ...EMPTY_UPDATE_STATE, ...(await api.get<Partial<UpdateState>>('/update')) }
}

// checkForUpdate asks the backend to re-read the release feed. It returns as
// soon as the check is queued; the result arrives over the event stream.
export async function checkForUpdate(): Promise<UpdateState> {
  return { ...EMPTY_UPDATE_STATE, ...(await api.post<Partial<UpdateState>>('/update/check', {})) }
}

// installUpdate applies the staged build and restarts the app. The response
// may never arrive — the server is on its way down — which is not a failure.
export async function installUpdate(): Promise<void> {
  await api.post('/update/install', {})
}

// dismissUpdate stops the prompt for the offered version. The download is kept,
// so accepting it later costs nothing.
export async function dismissUpdate(): Promise<void> {
  await api.post('/update/dismiss', {})
}
