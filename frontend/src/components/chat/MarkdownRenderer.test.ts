import { describe, expect, it } from 'vitest'
import { fixMarkdown } from './MarkdownRenderer'

describe('fixMarkdown', () => {
  it('normalizes compact headings and lists', () => {
    const output = fixMarkdown('##核心观点###数据概况-**文件名称**：records.csv-**字段定义**：姓名、地点')

    expect(output).toContain('## 核心观点')
    expect(output).toContain('### 数据概况')
    expect(output).toContain('- **文件名称**：records.csv')
    expect(output).toContain('- **字段定义**：姓名、地点')
  })

  it('repairs compact markdown tables from model output', () => {
    const output = fixMarkdown(
      '###表格摘要|字段A|字段B|字段C||------|------|------||值甲|值乙|300||值丙|值丁|200|',
    )

    expect(output).toContain('### 表格摘要')
    expect(output).toContain('| 字段A | 字段B | 字段C |')
    expect(output).toContain('| --- | --- | --- |')
    expect(output).toContain('| 值甲 | 值乙 | 300 |')
    expect(output).toContain('| 值丙 | 值丁 | 200 |')
  })

  it('keeps valid mermaid fences readable', () => {
    const output = fixMarkdown('```mermaidflowchart TD A-->B```')

    expect(output).toContain('```mermaid')
    expect(output).toContain('flowchart TD')
    expect(output).toMatch(/A\s*-->\s*B/)
    expect(output).toMatch(/```\s*$/)
  })

  it('does not rewrite fenced source code while repairing prose', () => {
    const output = fixMarkdown('说明 fooBar\n\n```ts\nconst fooBar = "aB"\n```')

    expect(output).toContain('const fooBar = "aB"')
    expect(output).not.toContain('const foo Bar')
    expect(output).not.toContain('"a B"')
  })

  it('removes chat template tokens and think tags', () => {
    const output = fixMarkdown('<|im_start|>assistant\n<think>hidden</think>\n##答案\n正文<|im_end|>')

    expect(output).not.toContain('<|im_start|>')
    expect(output).not.toContain('<think>')
    expect(output).not.toContain('hidden')
    expect(output).toContain('## 答案')
  })

  it('preserves all emoji without leaving unpaired surrogates', () => {
    const emojis = [
      '✅',
      '☑️',
      '✔️',
      '📌',
      '✨',
      '🛠️',
      '🚀',
      '🎯',
      '⚠️',
      '👋',
      '😊',
      '👍🏽',
      '👨‍💻',
      '❤️',
      '🇨🇳',
      '1️⃣',
    ]
    const output = fixMarkdown(`常用表情：${emojis.join(' ')}`)
    const hasUnpairedSurrogate = Array.from(output).some((character) => {
      const codePoint = character.codePointAt(0) ?? 0
      return codePoint >= 0xd800 && codePoint <= 0xdfff
    })

    for (const emoji of emojis) {
      expect(output).toContain(emoji)
    }
    expect(hasUnpairedSurrogate).toBe(false)
  })

  it('preserves decorative symbols as model-authored content', () => {
    const output = fixMarkdown('✅ 核心内容 • 📌 下一步 🚀')

    expect(output).toContain('✅')
    expect(output).toContain('•')
    expect(output).toContain('📌')
    expect(output).toContain('🚀')
  })
})
