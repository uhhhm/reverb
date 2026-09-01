/**
 * The find-and-replace behind batch renaming.
 *
 * It runs in the browser, not on the server: the dialog shows the user exactly
 * what every name will become, and what gets sent is those literal names. The
 * server never interprets a pattern, so what was approved is what is stored.
 */

export interface ReplaceRule {
  find: string
  replace: string
  matchCase: boolean
  /** Treats `find` as a regular expression, with `$1` available in `replace`. */
  useRegex: boolean
}

export const EMPTY_RULE: ReplaceRule = { find: '', replace: '', matchCase: false, useRegex: false }

export type CompiledRule =
  | { ok: true; apply: (value: string) => string }
  | { ok: false; error: string }

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * Turns a rule into a function, or reports why it cannot be. An invalid regex
 * is the user still typing one, so it is a message next to the field rather
 * than an error state for the dialog.
 */
export function compileRule(rule: ReplaceRule): CompiledRule {
  if (rule.find === '') return { ok: true, apply: (v) => v }
  const flags = rule.matchCase ? 'g' : 'gi'
  const source = rule.useRegex ? rule.find : escapeRegExp(rule.find)
  let re: RegExp
  try {
    re = new RegExp(source, flags)
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'invalid pattern' }
  }
  return {
    ok: true,
    // A fresh lastIndex per call: the same RegExp object is reused across every
    // row, and a sticky index left by one row would skip matches in the next.
    apply: (v) => {
      re.lastIndex = 0
      return v.replace(re, rule.replace)
    },
  }
}

export interface Change<T> {
  item: T
  field: string
  before: string
  after: string
}

/**
 * Applies a rule across many values and returns only what actually changes,
 * which is what the preview shows and what gets submitted. A rule that matches
 * nothing produces an empty list rather than a no-op write per row.
 */
export function previewChanges<T>(
  items: T[],
  fields: { name: string; get: (item: T) => string }[],
  rule: CompiledRule,
): Change<T>[] {
  if (!rule.ok) return []
  const out: Change<T>[] = []
  for (const item of items) {
    for (const f of fields) {
      const before = f.get(item) ?? ''
      const after = rule.apply(before)
      if (after !== before) out.push({ item, field: f.name, before, after })
    }
  }
  return out
}
