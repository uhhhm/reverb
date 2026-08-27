import { api } from './api'

export interface UpdateRelease {
  tag: string
  body: string
  assets: { name: string; url: string }[]
}

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

export async function fetchLatestRelease(repo: string = DEFAULT_REPO): Promise<UpdateRelease> {
  const res = await fetch(`https://api.github.com/repos/${repo}/releases/latest`, {
    headers: { Accept: 'application/vnd.github.v3+json' },
  })
  if (!res.ok) throw new Error(`github releases ${res.status}`)
  const json = (await res.json()) as {
    tag_name: string
    body: string
    assets: { name: string; browser_download_url: string }[]
  }
  return {
    tag: json.tag_name,
    body: json.body,
    assets: (json.assets ?? []).map((a) => ({ name: a.name, url: a.browser_download_url })),
  }
}

export async function checkForUpdate(
  currentVersion: string,
  repo: string = DEFAULT_REPO,
): Promise<string | null> {
  const rel = await fetchLatestRelease(repo)
  if (isNewer(currentVersion, rel.tag)) return rel.tag
  return null
}
