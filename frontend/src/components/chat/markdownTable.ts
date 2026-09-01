interface ParsedTableRow {
  cells: string[]
}

const isTableSeparatorCell = (cell: string) => /^:?-{3,}:?$/.test(cell.trim())

const splitUnescapedPipes = (line: string): string[] => {
  const cells: string[] = []
  let current = ''

  for (let index = 0; index < line.length; index += 1) {
    const character = line[index]
    if (character === '\\' && line[index + 1] === '|') {
      current += '\\|'
      index += 1
      continue
    }
    if (character === '|') {
      cells.push(current.trim())
      current = ''
      continue
    }
    current += character
  }

  cells.push(current.trim())
  return cells
}

const parseTableRow = (line: string): ParsedTableRow | null => {
  const trimmed = line.trim()
  if (!trimmed || !trimmed.includes('|')) {
    return null
  }

  const cells = splitUnescapedPipes(trimmed)
  if (trimmed.startsWith('|')) {
    cells.shift()
  }
  if (trimmed.endsWith('|') && !trimmed.endsWith('\\|')) {
    cells.pop()
  }

  return cells.length >= 2 ? { cells } : null
}

const trimTrailingEmptyCells = (cells: string[]) => {
  const trimmed = [...cells]
  while (trimmed.length > 2 && trimmed[trimmed.length - 1] === '') {
    trimmed.pop()
  }
  return trimmed
}

const fitTableRow = (cells: string[], columnCount: number): string[] | null => {
  if (cells.length > columnCount) {
    const extraCells = cells.slice(columnCount)
    if (extraCells.some((cell) => cell !== '')) {
      return null
    }
    return cells.slice(0, columnCount)
  }

  return [...cells, ...Array.from({ length: columnCount - cells.length }, () => '')]
}

const formatTableRow = (cells: string[]) => `| ${cells.join(' | ')} |`

/**
 * Canonicalize table blocks that already have a Markdown separator row.
 * Empty cells are preserved so a malformed trailing `||` cannot shift data
 * into the next column.
 */
export const normalizeMarkdownTableRows = (content: string): string => {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const header = parseTableRow(lines[index])
    const separator = parseTableRow(lines[index + 1] ?? '')
    if (!header || !separator) {
      normalized.push(lines[index])
      continue
    }

    const headerCells = trimTrailingEmptyCells(header.cells)
    const separatorCells = trimTrailingEmptyCells(separator.cells)
    if (
      headerCells.length < 2 ||
      separatorCells.length !== headerCells.length ||
      !separatorCells.every(isTableSeparatorCell)
    ) {
      normalized.push(lines[index])
      continue
    }

    const rows: string[][] = []
    let cursor = index + 2
    while (cursor < lines.length) {
      const row = parseTableRow(lines[cursor])
      if (!row || row.cells.every(isTableSeparatorCell)) {
        break
      }
      const fitted = fitTableRow(row.cells, headerCells.length)
      if (!fitted) {
        break
      }
      rows.push(fitted)
      cursor += 1
    }

    normalized.push(formatTableRow(headerCells))
    normalized.push(formatTableRow(headerCells.map(() => '---')))
    rows.forEach((row) => normalized.push(formatTableRow(row)))
    index = cursor - 1
  }

  return normalized.join('\n')
}
