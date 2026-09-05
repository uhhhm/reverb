import { describe, expect, it } from 'vitest'
import { normalize, encodeExternalId, decodeExternalId } from './trackRef'
import wire from '../../../internal/trackref/testdata/wire.json'

type Corpus = { cases: { name: string; external: { title: string; artist: string }; normalize?: { title: string; artist: string } }[] }
const corpora = import.meta.glob<Corpus>('../../../internal/matching/testdata/*.json', { eager: true, import: 'default' })

describe('shared Go/TypeScript identity contracts', () => {
  it('uses the backend matching corpus', () => {
    let checked = 0
    for (const corpus of Object.values(corpora)) {
      for (const c of corpus.cases) {
        if (!c.normalize) continue
        expect(normalize(c.external.title), c.name).toBe(c.normalize.title)
        expect(normalize(c.external.artist), c.name).toBe(c.normalize.artist)
        checked++
      }
    }
    expect(checked).toBeGreaterThan(0)
  })
  it.each(wire)('encodes and decodes $encoded', (c) => {
    expect(encodeExternalId(c.source, c.externalId)).toBe(c.encoded)
    expect(decodeExternalId(c.encoded)).toEqual({ source: c.source.trim(), externalId: c.externalId.trim() })
  })
})
