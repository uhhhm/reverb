import { describe, expect, it } from 'vitest'
import { reqFromResult } from './downloadApi'

describe('reqFromResult', () => {
	it('carries recommendation attribution into a library download', () => {
		expect(reqFromResult({
			source: 'deezer', externalId: '1', artist: 'A', title: 'T', album: '', durationMs: 1,
			type: 'track', recommendationOrigin: 'similarTracks',
		})).toMatchObject({ recommendationOrigin: 'similarTracks' })
	})
})
