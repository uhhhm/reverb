import { describe, expect, it } from 'vitest'
import { baseCommands, matchCommands } from './commands'
describe('commands', () => {
  it('always includes the Admin nav command (single-user)', () => { expect(baseCommands().map((c) => c.id)).toContain('nav-admin') })
  it('matches title before keywords case-insensitively', () => { const cmds = baseCommands(); expect(matchCommands('down', cmds)[0].id).toBe('panel-downloads'); expect(matchCommands('', cmds)).toHaveLength(cmds.length) })
  it('matches player verbs via keywords', () => expect(matchCommands('skip', baseCommands()).map((c) => c.id)).toContain('player-next'))
})