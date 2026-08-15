export interface FormattedRawOutput {
  text: string
  structured: boolean
  entries: number
}

function pretty(value: unknown) {
  return JSON.stringify(value, null, 2)
}

function matchingJsonEnd(source: string, start: number) {
  const first = source[start]
  if (first !== '{' && first !== '[') return -1
  const stack = [first]
  let quoted = false
  let escaped = false

  for (let index = start + 1; index < source.length; index += 1) {
    const character = source[index]
    if (quoted) {
      if (escaped) escaped = false
      else if (character === '\\') escaped = true
      else if (character === '"') quoted = false
      continue
    }
    if (character === '"') {
      quoted = true
      continue
    }
    if (character === '{' || character === '[') stack.push(character)
    if (character === '}' || character === ']') {
      const opening = stack.pop()
      if ((opening === '{' && character !== '}') || (opening === '[' && character !== ']')) return -1
      if (!stack.length) return index + 1
    }
  }
  return -1
}

/** Pretty-prints a JSON value or a concatenated JSON event stream without changing plain output. */
export function formatRawOutput(input: string): FormattedRawOutput {
  const source = input.trim()
  if (!source) return { text: '', structured: false, entries: 0 }

  try {
    return { text: pretty(JSON.parse(source) as unknown), structured: true, entries: 1 }
  } catch {
    // Codex emits adjacent JSON events, so continue with stream parsing.
  }

  const values: unknown[] = []
  let cursor = 0
  while (cursor < source.length) {
    while (/\s/.test(source[cursor] ?? '')) cursor += 1
    if (cursor >= source.length) break
    const end = matchingJsonEnd(source, cursor)
    if (end < 0) return { text: input, structured: false, entries: 0 }
    try {
      values.push(JSON.parse(source.slice(cursor, end)) as unknown)
    } catch {
      return { text: input, structured: false, entries: 0 }
    }
    cursor = end
  }

  if (!values.length) return { text: input, structured: false, entries: 0 }
  return { text: values.map(pretty).join('\n\n'), structured: true, entries: values.length }
}
