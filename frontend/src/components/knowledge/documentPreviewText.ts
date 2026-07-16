const MARKDOWN_HEADING_ONLY = /^#{1,6}$/
const MARKDOWN_HEADING = /^#{1,6}\s+\S/
const MARKDOWN_BLOCK = /^(?:[-*+]\s+|\d+[.)]\s+|>\s+|```|---+$)/
const ASCII_WORD = /[A-Za-z0-9]/
const CLOSING_PUNCTUATION = /^[,.;:!?，。；：！？、）】》”’]/
const OPENING_PUNCTUATION = /[（【《“‘]$/
const RAW_PREVIEW_EXTENSION = /\.(?:csv|tsv|json|jsonl|ndjson|ya?ml|xml|sql|log)$/i

export const shouldUseRawDocumentPreview = (documentName: string) => (
  RAW_PREVIEW_EXTENSION.test(documentName.trim())
)

const appendWrappedLine = (current: string, next: string) => {
  if (!current) return next

  const currentLast = current.at(-1) ?? ''
  const nextFirst = next.at(0) ?? ''
  const needsSpace = ASCII_WORD.test(currentLast) && ASCII_WORD.test(nextFirst)

  if (CLOSING_PUNCTUATION.test(next) || OPENING_PUNCTUATION.test(current)) {
    return `${current}${next}`
  }

  return `${current}${needsSpace ? ' ' : ''}${next}`
}

export const formatDocumentPreviewText = (value: string) => {
  const lines = value
    .replace(/\r\n?/g, '\n')
    .split('\n')
    .map((line) => line.trim().replace(/[\t\f\v ]+/g, ' '))

  const blocks: string[] = []
  let paragraph = ''

  const flushParagraph = () => {
    if (!paragraph) return
    blocks.push(paragraph)
    paragraph = ''
  }

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]

    if (!line) {
      flushParagraph()
      continue
    }

    if (MARKDOWN_HEADING_ONLY.test(line)) {
      flushParagraph()
      const nextLine = lines[index + 1]
      if (nextLine) {
        blocks.push(`${line} ${nextLine}`)
        index += 1
      } else {
        blocks.push(line)
      }
      continue
    }

    if (MARKDOWN_HEADING.test(line) || MARKDOWN_BLOCK.test(line)) {
      flushParagraph()
      blocks.push(line)
      continue
    }

    paragraph = appendWrappedLine(paragraph, line)
  }

  flushParagraph()
  return blocks.join('\n\n')
}
