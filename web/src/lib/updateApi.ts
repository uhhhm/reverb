import { api } from './api'

// Fallback only. The repository is server-configured (REVERB_UPDATE_REPO /
// --update-repo) and reported by /version as updateRepo.
const DEFAULT_REPO = 'uhhhm/reverb'

export interface VersionInfo {
  version: string
  // GitHub owner/name to poll for releases; '' when updates are disabled.
  updateRepo: string
}

export async function fetchVersionInfo(): Promise<VersionInfo> {
  const data = await api.get<{ version: string; updateRepo?: string }>('/version')
  return { version: data.version, updateRepo: data.updateRepo ?? DEFAULT_REPO }
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
