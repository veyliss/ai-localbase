import { describe, expect, it } from 'vitest'
import { formatDocumentPreviewText, shouldUseRawDocumentPreview } from './documentPreviewText'

describe('formatDocumentPreviewText', () => {
  it('merges PDF line wraps while keeping paragraph breaks', () => {
    expect(formatDocumentPreviewText('示例机构\n是一个示例机构。\n\n机构位于\n示例城市。')).toBe(
      '示例机构是一个示例机构。\n\n机构位于示例城市。',
    )
  })

  it('joins isolated markdown heading markers with their titles', () => {
    expect(formatDocumentPreviewText('#\n示例机构简介\n\n##\n机构概况')).toBe(
      '# 示例机构简介\n\n## 机构概况',
    )
  })

  it('preserves spaces between wrapped latin words', () => {
    expect(formatDocumentPreviewText('Example\nOrganization\n位于示例城市')).toBe(
      'Example Organization位于示例城市',
    )
  })

  it('keeps structured document types in raw mode by default', () => {
    expect(shouldUseRawDocumentPreview('records.csv')).toBe(true)
    expect(shouldUseRawDocumentPreview('config.JSON')).toBe(true)
    expect(shouldUseRawDocumentPreview('report.pdf')).toBe(false)
  })
})
