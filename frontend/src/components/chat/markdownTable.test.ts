import { describe, expect, it } from 'vitest'
import { normalizeMarkdownTableRows } from './markdownTable'

describe('normalizeMarkdownTableRows', () => {
  it('removes redundant trailing separators without shifting empty cells', () => {
    const output = normalizeMarkdownTableRows([
      '| 姓名 | 年龄 | 备注 ||',
      '| --- | --- | --- ||',
      '| 张三 | 20 ||',
      '| 李四 |  | 已离职 |',
    ].join('\n'))

    expect(output).toBe([
      '| 姓名 | 年龄 | 备注 |',
      '| --- | --- | --- |',
      '| 张三 | 20 |  |',
      '| 李四 |  | 已离职 |',
    ].join('\n'))
    expect(output).not.toContain('||')
  })

  it('preserves escaped pipes inside a table cell', () => {
    const output = normalizeMarkdownTableRows([
      '| 字段 | 说明 |',
      '| --- | --- |',
      '| 命令 | A \\| B |',
    ].join('\n'))

    expect(output).toContain('A \\| B')
    expect(output).toContain('| 命令 | A \\| B |')
  })

  it('leaves pipe-shaped prose alone when it has no separator row', () => {
    const prose = '路径 A | 路径 B | 路径 C'
    expect(normalizeMarkdownTableRows(prose)).toBe(prose)
  })
})
