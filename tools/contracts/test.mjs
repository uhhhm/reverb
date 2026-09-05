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
  execFileSync('go', ['test', './internal/api', '-run', '^TestDownloadHTTPContract$', '-count=1'], {
    env: { ...process.env, REVERB_CONTRACT_OUTPUT: output }, stdio: 'inherit',
  })
  const samples = JSON.parse(readFileSync(output, 'utf8'))
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
  for (const event of samples.events) validate(doc.components.schemas.RealtimeEvent, event)
  console.log('Download HTTP and event contracts passed')
} finally { rmSync(dir, { recursive: true, force: true }) }
