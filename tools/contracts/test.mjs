import { readFileSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import YAML from 'yaml'
import Ajv from 'ajv'
process.chdir(fileURLToPath(new URL('../..', import.meta.url)))
const dir = mkdtempSync(join(tmpdir(), 'reverb-contract-'))
try {
  const output = join(dir, 'responses.json')
  const mobileOutput = join(dir, 'mobile.json')
  execFileSync('go', ['test', './internal/api', '-run', '^(TestDownloadHTTPContract|TestMobileHTTPContract)$', '-count=1'], {
    env: { ...process.env, REVERB_CONTRACT_OUTPUT: output, REVERB_MOBILE_CONTRACT_OUTPUT: mobileOutput }, stdio: 'inherit',
  })
  const samples = JSON.parse(readFileSync(output, 'utf8'))
  const mobile = JSON.parse(readFileSync(mobileOutput, 'utf8'))
  const doc = YAML.parse(readFileSync('internal/api/openapi.yaml', 'utf8'))
  const ajv = new Ajv({ strict: false, validateFormats: false })
  ajv.addSchema({ $id: 'reverb', components: doc.components })
  function validate(schema, value) {
    const check = ajv.compile({ ...schema, components: doc.components })
    assert.ok(check(value), JSON.stringify(check.errors))
  }
  for (const [sample, path, method] of [
    ['create', '/downloads', 'post'], ['list', '/downloads', 'get'],
    ['queue', '/downloads/queue', 'get'], ['retry', '/downloads/{id}/retry', 'post'],
  ]) validate(doc.paths[path][method].responses['200'].content['application/json'].schema, samples[sample])
  validate(doc.paths['/downloads'].post.requestBody.content['application/json'].schema, samples.request)
  validate(doc.paths['/player/{session}/enqueue'].post.responses['200'].content['application/json'].schema, samples.player)
  for (const event of samples.events) validate(doc.components.schemas.RealtimeEvent, event)
  const okSchema = (path, method = 'get') => doc.paths[path][method].responses['200'].content['application/json'].schema
  for (const [sample, path, method] of [
    ['health', '/health'], ['devices', '/pairing/devices'], ['catalog', '/library/catalog/tracks'], ['playlists', '/playlists'], ['playlist', '/playlists/{id}'],
    ['offlineList', '/offline-set'], ['offlineStatus', '/offline-set/status'],
    ['offlinePut', '/offline-set/{playlistId}', 'put'], ['offlineDelete', '/offline-set/{playlistId}', 'delete'],
  ]) validate(okSchema(path, method), mobile[sample])
  console.log('Download, catalog, player, playlist and offline set HTTP and event contracts passed')
} finally { rmSync(dir, { recursive: true, force: true }) }
