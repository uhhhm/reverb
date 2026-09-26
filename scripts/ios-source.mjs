#!/usr/bin/env node
// Writes the SideStore/AltStore source for the iOS app (ADR 0004): the app's
// metadata and its released versions, newest first. The release workflow runs
// it with the source it last published, so each release adds one version and
// earlier ones stay installable.
//
//   node scripts/ios-source.mjs --repo owner/name --version 1.2.0 \
//     --date 2026-09-26 --size 123456 [--previous source.json] --out source.json
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { parseArgs } from 'node:util'

const { values: a } = parseArgs({
  options: {
    repo: { type: 'string' },
    version: { type: 'string' },
    date: { type: 'string' },
    size: { type: 'string' },
    previous: { type: 'string' },
    out: { type: 'string' },
  },
})
const fail = (msg) => {
  console.error(`ios-source: ${msg}`)
  process.exit(1)
}
if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(a.repo ?? '')) fail('--repo must be owner/name')
const version = (a.version ?? '').replace(/^v/, '')
if (!/^\d+\.\d+\.\d+$/.test(version)) fail('--version must be a stable x.y.z release')
if (!/^\d{4}-\d{2}-\d{2}$/.test(a.date ?? '')) fail('--date must be YYYY-MM-DD')
const size = Number(a.size)
if (!Number.isInteger(size) || size <= 0) fail('--size must be the IPA size in bytes')
if (!a.out) fail('--out is required')

const [owner] = a.repo.split('/')
const bundleIdentifier = 'io.github.uhhhm.reverb'
// Kept so an owner can roll back a release that misbehaves for them.
const KEEP_VERSIONS = 10

let previous = []
if (a.previous && existsSync(a.previous)) {
  const old = JSON.parse(readFileSync(a.previous, 'utf8'))
  previous = old.apps?.find((app) => app.bundleIdentifier === bundleIdentifier)?.versions ?? []
}
const versions = [
  {
    version,
    date: a.date,
    localizedDescription: `Reverb ${version}. Release notes: https://github.com/${a.repo}/releases/tag/v${version}`,
    downloadURL: `https://github.com/${a.repo}/releases/download/v${version}/Reverb.ipa`,
    size,
    minOSVersion: '17.0',
  },
  ...previous.filter((v) => v.version !== version),
].slice(0, KEEP_VERSIONS)

const source = {
  name: 'Reverb',
  identifier: `io.github.${owner.toLowerCase()}.reverb.source`,
  subtitle: 'Self-hosted music, on your iPhone.',
  website: `https://github.com/${a.repo}`,
  iconURL: `https://github.com/${owner}.png`,
  apps: [
    {
      name: 'Reverb',
      bundleIdentifier,
      developerName: owner,
      subtitle: 'Your Reverb library, offline and in sync.',
      localizedDescription:
        'Reverb for iPhone pairs with your Reverb devices, keeps playlists offline, and syncs plays and edits over your network or VPN.',
      iconURL: `https://github.com/${owner}.png`,
      category: 'entertainment',
      appPermissions: {
        entitlements: [],
        privacy: {
          NSCameraUsageDescription: 'Reverb uses the camera to scan the pairing code your computer shows.',
          NSLocalNetworkUsageDescription: 'Reverb finds and syncs with Reverb on your other devices over your network.',
        },
      },
      versions,
    },
  ],
  news: [],
}
writeFileSync(a.out, JSON.stringify(source, null, 2) + '\n')
