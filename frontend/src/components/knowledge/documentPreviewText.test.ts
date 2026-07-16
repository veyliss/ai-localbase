import { describe, expect, it } from 'vitest'
import { formatDocumentPreviewText, shouldUseRawDocumentPreview } from './documentPreviewText'

describe('formatDocumentPreviewText', () => {
  it('merges PDF line wraps while keeping paragraph breaks', () => {
    expect(formatDocumentPreviewText('武汉大学\n是全国重点大学。\n\n学校位于\n武汉。')).toBe(
      '武汉大学是全国重点大学。\n\n学校位于武汉。',
    )
  })

  it('joins isolated markdown heading markers with their titles', () => {
    expect(formatDocumentPreviewText('#\n武汉大学简介\n\n##\n学校概况')).toBe(
      '# 武汉大学简介\n\n## 学校概况',
    )
  })

  it('preserves spaces between wrapped latin words', () => {
    expect(formatDocumentPreviewText('Wuhan\nUniversity\n位于武汉')).toBe(
      'Wuhan University位于武汉',
    )
  })

  it('keeps structured document types in raw mode by default', () => {
    expect(shouldUseRawDocumentPreview('records.csv')).toBe(true)
    expect(shouldUseRawDocumentPreview('config.JSON')).toBe(true)
    expect(shouldUseRawDocumentPreview('report.pdf')).toBe(false)
  })
})
