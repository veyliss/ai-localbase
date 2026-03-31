import React, { useEffect, useMemo, useRef, useState } from 'react'
import mermaid from 'mermaid'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { AppConfig, Conversation, DocumentItem, KnowledgeBase } from '../App'

/**
 * 修复 LLM 输出的 Markdown 格式问题：
 * 1. `##标题` → `## 标题`（标题符号后缺空格）
 * 2. 标题前无空行时补空行，确保解析器正确识别
 * 3. 尝试拆分被压成单行的表格、列表与编号段落
 * 4. 将目录树/路径结构包裹成代码块，避免半渲染
 * 5. 针对 Ollama 常见的“中文段落 + checklist + 分隔线粘连”做额外修复
 */
function looksLikePseudoTableHeader(cell: string): boolean {
  const trimmed = cell.trim()
  if (!trimmed || trimmed.length > 18) {
    return false
  }

  return !/[，。；：:,.!?()（）\[\]]/.test(trimmed)
}

function renumberOrderedListBlocks(content: string): string {
  const lines = content.split('\n')
  let counter = 0

  return lines
    .map((line) => {
      const trimmed = line.trim()
      if (!trimmed) {
        return line
      }

      if (/^(#{1,6}|[-*+]\s|>\s|```)/.test(trimmed)) {
        counter = 0
        return line
      }

      if (/^\d+[.)、]\s+/.test(trimmed)) {
        counter += 1
        return line.replace(/^(\s*)\d+[.)、]\s+/, `$1${counter}. `)
      }

      if (/^[A-Z]\./.test(trimmed) || /^第[一二三四五六七八九十]+/.test(trimmed)) {
        counter = 0
      }

      return line
    })
    .join('\n')
}

function normalizePseudoStructuredLine(line: string): string {
  const trimmed = line.trim()
  if (!trimmed) {
    return ''
  }

  if (/^\|(?:\s*:?-{3,}:?\s*\|)+\s*$/.test(trimmed)) {
    return line
  }

  if (/^\|.*\|\s*$/.test(trimmed)) {
    return line
  }

  if (/^[|:\-\s]+$/.test(trimmed)) {
    return ''
  }

  const pipeCount = (trimmed.match(/\|/g) ?? []).length
  if (pipeCount < 3) {
    return line
  }

  const cells = trimmed
    .split('|')
    .map((cell) => cell.trim())
    .filter(Boolean)
    .filter((cell) => !/^:?-{3,}:?$/.test(cell))

  if (cells.length < 2) {
    return line
  }

  if (cells.length >= 4 && cells.length % 2 === 0) {
    const half = cells.length / 2
    const headers = cells.slice(0, half)
    const values = cells.slice(half)

    if (headers.every((cell) => looksLikePseudoTableHeader(cell))) {
      return headers.map((header, index) => `- ${header}：${values[index]}`).join('\n')
    }
  }

  return cells.map((cell) => `- ${cell}`).join('\n')
}

function normalizeDeliverableSection(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim()

    if (!line) {
      normalized.push(lines[index])
      continue
    }

    const cells = line
      .split('|')
      .map((cell) => cell.trim())
      .filter(Boolean)

    const isDeliverableHeader =
      cells.length === 3 &&
      ((cells[0] === '模块' && cells[1] === '交付物' && cells[2] === '基础功能') ||
        (cells[0] === '阶段' && cells[1] === '任务' && cells[2] === '优先级'))

    if (!isDeliverableHeader) {
      normalized.push(lines[index])
      continue
    }

    const labels = cells
    let cursor = index + 1

    while (cursor + 2 < lines.length) {
      const row = [lines[cursor].trim(), lines[cursor + 1].trim(), lines[cursor + 2].trim()]

      if (row.some((value) => !value)) {
        break
      }

      if (row.some((value) => value.includes('|'))) {
        break
      }

      if (/^(##|###|第\d+步|根据|总结|核心|技术栈|系统架构|关键约束|如果需)/.test(row[0])) {
        break
      }

      normalized.push(`- ${labels[0]}：${row[0]}`)
      normalized.push(`  - ${labels[1]}：${row[1]}`)
      normalized.push(`  - ${labels[2]}：${row[2]}`)
      cursor += 3
      index += 3
    }
  }

  return normalized.join('\n')
}

function normalizePainSolutionSection(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const current = lines[index].trim()
    const next = lines[index + 1]?.trim() ?? ''

    const currentCells = current
      .split('|')
      .map((cell) => cell.trim())
      .filter(Boolean)

    const isPainSolutionHeader =
      currentCells.length >= 3 &&
      currentCells[0] === '问题' &&
      currentCells[1] === '解决方案'

    if (!isPainSolutionHeader) {
      normalized.push(lines[index])
      continue
    }

    normalized.push('- 问题：' + currentCells[2])
    if (next) {
      normalized.push('- 解决方案：' + next)
      index += 1
    }

    let cursor = index + 1
    while (cursor + 1 < lines.length) {
      const problem = lines[cursor].trim()
      const solution = lines[cursor + 1].trim()

      if (!problem || !solution) {
        break
      }

      if (problem.includes('|') || solution.includes('|')) {
        break
      }

      if (/^(根据|核心|技术栈|系统架构|关键约束|如果需|总结)/.test(problem)) {
        break
      }

      normalized.push('- 问题：' + problem)
      normalized.push('- 解决方案：' + solution)
      cursor += 2
      index += 2
    }
  }

  return normalized.join('\n')
}

function normalizeStepSections(content: string): string {
  let normalized = content

  normalized = normalized.replace(/(^|\n)(实施路线图（简化版）|实施路径|实施步骤|步骤规划)\s*[:：]?\s*/g, '$1## $2\n\n')
  normalized = normalized.replace(/(^|\n)(阶段|任务|优先级)\s*(?=\n|$)/g, '$1')
  normalized = normalized.replace(/(^|\n)(第\d+步)\s*[:：]?\s*/g, '$1### $2\n\n')
  normalized = normalized.replace(/(^|\n)(MVP 核心功能验证|模块架构深化|接口与前端交互设计|UI 与交互优化|测试与迭代优化)\s*[:：]?\s*/g, '$1- $2：')
  normalized = normalized.replace(/(^|\n)([^\n：]{2,20})\s*[：:]\s*(☆☆☆|★★★|高|中|低)\s*(?=\n|$)/g, '$1- $2：$3')
  normalized = normalized.replace(/(^|\n)([^\n：]{2,20})\s*(☆☆☆|★★★)\s*(?=\n|$)/g, '$1- $2：$3')

  return normalized
}

function sanitizeMermaidLine(line: string): string {
  let sanitized = line.trim()
  sanitized = sanitized.replace(/^```+/, '')
  sanitized = sanitized.replace(/^mermaid/, '')
  sanitized = sanitized.replace(/```$/, '')
  sanitized = sanitized.replace(/<\/?span[^>]*>/g, '')
  sanitized = sanitized.replace(/<[^>]+>/g, '')
  sanitized = sanitized.replace(/\*\*/g, '')
  sanitized = sanitized.replace(/%%.*/g, '')
  sanitized = sanitized.replace(/classDef([A-Za-z0-9_]+)fill:/g, 'classDef $1 fill:')
  sanitized = sanitized.replace(/style([A-Za-z0-9_]+)fill:/g, 'style $1 fill:')
  sanitized = sanitized.replace(/style\s+([A-Za-z0-9_]+)fill:/g, 'style $1 fill:')
  sanitized = sanitized.replace(/class\s+Def/g, 'classDef ')
  sanitized = sanitized.replace(/class\s+([A-Za-z0-9_]+)fill:/g, 'classDef $1 fill:')
  sanitized = sanitized.replace(/flowchartTD/g, 'flowchart TD')
  sanitized = sanitized.replace(/flowchartLR/g, 'flowchart LR')
  sanitized = sanitized.replace(/graphTD/g, 'graph TD')
  sanitized = sanitized.replace(/graphLR/g, 'graph LR')
  sanitized = sanitized.replace(/mermaidflowchart\s*TD/g, 'flowchart TD')
  sanitized = sanitized.replace(/mermaidflowchart\s*LR/g, 'flowchart LR')
  sanitized = sanitized.replace(/mermaidgraph\s*TD/g, 'graph TD')
  sanitized = sanitized.replace(/mermaidgraph\s*LR/g, 'graph LR')
  sanitized = sanitized.replace(/;+\s*$/, '')
  return sanitized.trim()
}

function rebuildCompressedMermaid(lines: string[]): string[] {
  const source = lines.join(' ')
  if (!source) {
    return []
  }

  let rebuilt = source
    .replace(/^```mermaid\s*/i, '')
    .replace(/```$/i, '')
    .replace(/mermaidflowchart\s*TD/gi, 'flowchart TD\n')
    .replace(/mermaidgraph\s*TD/gi, 'graph TD\n')
    .replace(/mermaidflowchart\s*LR/gi, 'flowchart LR\n')
    .replace(/mermaidgraph\s*LR/gi, 'graph LR\n')
    .replace(/(flowchart\s+(?:TD|LR)|graph\s+(?:TD|LR)|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|mindmap|timeline)/g, '\n$1\n')
    .replace(/%%\s*/g, '\n%% ')
    .replace(/end\s*subgraph/gi, 'end\nsubgraph ')
    .replace(/endsubgraph/gi, 'end\nsubgraph ')
    .replace(/(subgraph\s+[A-Za-z0-9_\-]+\[[^\]]+\])/g, '\n$1\n')
    .replace(/(subgraph\s+[A-Za-z0-9_\-]+)/g, '\n$1\n')
    .replace(/(classDef\s+[A-Za-z0-9_]+\s+fill:[^;]+;)/g, '\n$1\n')
    .replace(/(style\s+[A-Za-z0-9_]+\s+fill:[^;]+;)/g, '\n$1\n')
    .replace(/(classDef[A-Za-z0-9_]+fill:[^;]+;)/g, '\n$1\n')
    .replace(/(style[A-Za-z0-9_]+fill:[^;]+;)/g, '\n$1\n')
    .replace(/([A-Za-z0-9_]+\[[^\]]+\])(?=[A-Za-z0-9_]+\[[^\]]+\])/g, '$1\n')
    .replace(/([A-Za-z0-9_]+\([^\)]*\))(?=[A-Za-z0-9_]+\([^\)]*\))/g, '$1\n')
    .replace(/([A-Za-z0-9_]+\{[^\}]*\})(?=[A-Za-z0-9_]+\{[^\}]*\})/g, '$1\n')
    .replace(/([A-Za-z0-9_\]\)\}])\s*(-->|==>|-.->)\s*([A-Za-z0-9_\[\(\{])/g, '$1 $2 $3')
    .replace(/([\]\)\}])\s*(?=[A-Za-z0-9_]+(?:\[|\(|\{|-->|==>|-.->))/g, '\n')
    .replace(/(;)(?=\s*(?:classDef|style|subgraph|end|[A-Za-z0-9_]+\[|[A-Za-z0-9_]+\{|[A-Za-z0-9_]+\(|[A-Za-z0-9_]+-->))/g, '$1\n')
    .replace(/((?:-->|==>|-.->)\s*[A-Za-z0-9_]+(?:\[[^\]]*\]|\([^\)]*\)|\{[^\}]*\}))(?!\s*(?:classDef|style|subgraph|end|%%|$))/g, '$1\n')
    .replace(/([A-Za-z0-9_]+(?:-->|==>|-.->)[A-Za-z0-9_]+)(?=[A-Za-z0-9_]+(?:-->|==>|-.->))/g, '$1\n')
    .replace(/(end)(?=\s*(?:[A-Za-z0-9_]+\[|[A-Za-z0-9_]+\{|[A-Za-z0-9_]+\(|subgraph|classDef|style|%%))/g, '$1\n')
    .replace(/(\w+)(-->|==>|-.->)(\w+)/g, '$1 $2 $3')
    .replace(/\]\s*(?=[A-Z][A-Za-z0-9_]*(?:-->|\[|\{|\())/g, ']\n')
    .replace(/\}\s*(?=[A-Z][A-Za-z0-9_]*(?:-->|\[|\{|\())/g, '}\n')
    .replace(/\)\s*(?=[A-Z][A-Za-z0-9_]*(?:-->|\[|\{|\())/g, ')\n')
    .replace(/\s{2,}/g, ' ')
    .replace(/\n{2,}/g, '\n')
    .trim()

  return rebuilt
    .split('\n')
    .map((line) => sanitizeMermaidLine(line))
    .filter(Boolean)
}

function isValidMermaidLine(line: string): boolean {
  if (!line) {
    return false
  }

  return /^(flowchart\s+(TD|LR)|graph\s+(TD|LR)|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|mindmap|timeline|subgraph|end|style\s|classDef\s|class\s|linkStyle\s|[A-Za-z0-9_\-\u4e00-\u9fa5]+\s*(\(|\{|\[)|[A-Za-z0-9_\-\u4e00-\u9fa5]+\s*(-{1,2}|={1,2}|\.-)>|[A-Za-z0-9_\-\u4e00-\u9fa5]+\s+-->|%%)/.test(
    line,
  )
}

function normalizeMermaidSection(content: string): string {
  if (!/```mermaid/i.test(content)) {
    return content
  }

  const lines = content.split('\n')
  const normalized: string[] = []
  let mermaidBuffer: string[] = []
  let collecting = false

  const flushMermaidBuffer = () => {
    const rawLines = mermaidBuffer.map((line) => line.trim()).filter(Boolean)
    const rebuiltLines = rebuildCompressedMermaid(rawLines)
    const sanitizedLines = rebuiltLines.filter((line) => isValidMermaidLine(line))
    const outputLines = sanitizedLines.length > 0 ? sanitizedLines : rebuiltLines.length > 0 ? rebuiltLines : rawLines

    normalized.push('```mermaid')
    normalized.push(...outputLines)
    normalized.push('```')
    mermaidBuffer = []
  }

  for (const rawLine of lines) {
    const trimmed = rawLine.trim()

    if (trimmed.startsWith('```mermaid')) {
      collecting = true
      const inlineContent = sanitizeMermaidLine(trimmed)
      if (inlineContent) {
        mermaidBuffer.push(inlineContent)
      }
      continue
    }

    if (trimmed === '```' && collecting) {
      flushMermaidBuffer()
      collecting = false
      continue
    }

    if (collecting) {
      const cleaned = sanitizeMermaidLine(trimmed)
      if (cleaned) {
        mermaidBuffer.push(cleaned)
      }
      continue
    }

    normalized.push(rawLine)
  }

  if (collecting) {
    flushMermaidBuffer()
  }

  return normalized.join('\n')
}

function normalizeSummarySections(content: string): string {
  let normalized = content

  normalized = normalized.replace(
    /(^|\n)(当前知识库的核心观点总结如下|最关键的结论总结如下|核心观点总结如下|核心结论如下)\s*[:：]?\s*/g,
    '$1## 核心结论\n\n',
  )
  normalized = normalized.replace(/(^|\n)(核心结论|关键结论)\s*(\d+[.)、])/g, '$1## $2\n\n$3')
  normalized = normalized.replace(
    /(^|\n)(总结|结论总结|关键决策点|下一步行动|下一步建议)\s*[:：]?\s*/g,
    '$1## $2\n\n',
  )

  normalized = normalized.replace(/([。；])\s*(\d+[.)、]\s+)/g, '$1\n\n$2')
  normalized = normalized.replace(/([^\n])(\d+[.)、]\s+)/g, '$1\n$2')
  normalized = normalized.replace(/(\d+[.)、][^\n。！？]*?)(?=\s+\d+[.)、]\s+)/g, '$1\n')
  normalized = normalized.replace(/([。；])\s*(##\s)/g, '$1\n\n$2')
  normalized = normalized.replace(/([^\n])(是否将|建议采用|建议优先|需要确认|优先实现)/g, '$1\n\n$2')
  normalized = normalized.replace(/([^\n])(产品定位|解决的核心问题|实施路径|技术选型建议|后续演进方向|用户价值主张|目标用户群体|技术架构分层)\s*-/g, '$1\n$2 -')

  return normalized
}

function normalizeVisualSeparators(content: string): string {
  let normalized = content

  normalized = normalized.replace(/(^|\n)\s*([=]{3,}|[-]{3,}|_{3,}|[─]{3,}|[━]{3,}|[—]{3,})\s*(?=\n|$)/g, '$1---')
  normalized = normalized.replace(/(^|\n)\s*(结论|答案|统计依据|计算过程|关键数据|补充说明|注意事项|下一步)\s*[：:]\s*/g, '$1### $2\n\n')
  normalized = normalized.replace(/(^|\n)\s*(一图概览|核心结论|最终答案|统计结果|主要发现)\s*(?=\n|$)/g, '$1## $2\n\n')
  normalized = normalized.replace(/([\u4e00-\u9fa5A-Za-z0-9）)])\s*(---)\s*(?=[\u4e00-\u9fa5A-Za-z0-9（(])/g, '$1\n\n$2\n\n')

  return normalized
}

function normalizeHeadingAttachedTable(content: string): string {
  let normalized = content
  normalized = normalized.replace(/(#{2,6}\s[^\n|]+)(\|(?=[^\n|]+\|[^\n|]+\|))/g, '$1\n\n$2')
  normalized = normalized.replace(/(\|[^\n]+\|)(#{2,6}\s)/g, '$1\n\n$2')
  normalized = normalized.replace(/(#{3,6}\s)([^\n|]+\|[^\n|]+\|[^\n|]+)(?=\n|$)/g, '$1$2')
  return normalized
}

const suggestionFieldAliases: Record<string, string[]> = {
  category: ['类别', '分类', '类型', '方向', '主题', '模块', '方案'],
  summary: ['说明', '概述', '结论', '判断', '特点', '核心观点'],
  recommendation: ['建议', '行动建议', '推荐', '做法', '策略'],
  scenario: ['适用场景', '适用情况', '适用对象', '适用阶段', '适配场景'],
  pros: ['优点', '优势', '收益', '好处'],
  cons: ['缺点', '风险', '不足', '限制', '注意事项'],
}

const suggestionFieldLabels: Record<string, string> = {
  category: '类别',
  summary: '说明',
  recommendation: '建议',
  scenario: '适用场景',
  pros: '优点',
  cons: '注意事项',
}

function findSuggestionFieldKey(label: string): string | null {
  const normalizedLabel = label.trim()
  for (const [key, aliases] of Object.entries(suggestionFieldAliases)) {
    if (aliases.includes(normalizedLabel)) {
      return key
    }
  }
  return null
}

function parseSuggestionSegments(line: string): Array<{ key: string; label: string; value: string }> {
  const cleaned = line.trim()
  if (!cleaned) {
    return []
  }

  const matches = [...cleaned.matchAll(/(类别|分类|类型|方向|主题|模块|方案|说明|概述|结论|判断|特点|核心观点|建议|行动建议|推荐|做法|策略|适用场景|适用情况|适用对象|适用阶段|适配场景|优点|优势|收益|好处|缺点|风险|不足|限制|注意事项)\s*[：:]/g)]
  if (matches.length < 2) {
    return []
  }

  const segments: Array<{ key: string; label: string; value: string }> = []
  for (let index = 0; index < matches.length; index += 1) {
    const match = matches[index]
    const label = match[1]
    const key = findSuggestionFieldKey(label)
    if (!key) {
      continue
    }
    const start = match.index! + match[0].length
    const end = index + 1 < matches.length ? matches[index + 1].index! : cleaned.length
    const value = cleaned.slice(start, end).trim().replace(/^[，、；;]+|[，、；;]+$/g, '')
    if (!value) {
      continue
    }
    segments.push({ key, label, value })
  }

  return segments
}

function normalizeSuggestionCardBlocks(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const current = lines[index].trim()
    const segments = parseSuggestionSegments(current)

    if (segments.length < 2) {
      normalized.push(lines[index])
      continue
    }

    const blockRows: string[] = []
    let cursor = index
    while (cursor < lines.length) {
      const line = lines[cursor].trim()
      const rowSegments = parseSuggestionSegments(line)
      if (rowSegments.length < 2) {
        break
      }
      blockRows.push(
        rowSegments
          .map((segment) => `${suggestionFieldLabels[segment.key] ?? segment.label}：${segment.value}`)
          .join(' | '),
      )
      cursor += 1
    }

    if (blockRows.length >= 2) {
      normalized.push('```advice-cards')
      normalized.push(...blockRows)
      normalized.push('```')
      index = cursor - 1
      continue
    }

    normalized.push(lines[index])
  }

  return normalized.join('\n')
}

function normalizeLooseTableBlocks(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const current = lines[index].trim()
    const next = lines[index + 1]?.trim() ?? ''

    const currentCells = current.split('|').map((cell) => cell.trim()).filter(Boolean)
    const nextCells = next.split('|').map((cell) => cell.trim()).filter(Boolean)
    const looksLikeTableHeader = currentCells.length >= 3 && nextCells.length === currentCells.length && !/^:?-{3,}:?$/.test(nextCells[0])

    if (!looksLikeTableHeader) {
      normalized.push(lines[index])
      continue
    }

    normalized.push(`| ${currentCells.join(' | ')} |`)
    normalized.push(`| ${currentCells.map(() => '---').join(' | ')} |`)
    index += 1

    while (index + 1 < lines.length) {
      const rowLine = lines[index + 1].trim()
      if (!rowLine || /^(##|###|>|- |\d+\.)/.test(rowLine)) {
        break
      }
      const rowCells = rowLine.split('|').map((cell) => cell.trim()).filter(Boolean)
      if (rowCells.length !== currentCells.length) {
        break
      }
      normalized.push(`| ${rowCells.join(' | ')} |`)
      index += 1
    }
  }

  return normalized.join('\n')
}

function isMarkdownSeparatorCell(cell: string): boolean {
  return /^:?-{3,}:?$/.test(cell.trim())
}

function containsDenseTableSeparator(line: string): boolean {
  return /\|\s*:?-{3,}:?\s*(?=\||$)/.test(line) || /-{3,}/.test(line)
}

function normalizeInlineMarkdownTables(content: string): string {
  const lines = content.split('\n')
  const normalized = lines.map((line) => {
    const trimmed = line.trim()
    if (!trimmed.includes('|')) {
      return line
    }

    const rawTokens = trimmed.split('|').map((cell) => cell.trim())
    const tokens = rawTokens.filter(Boolean)
    if (tokens.length < 6) {
      return line
    }

    let sepStart = -1
    let sepCount = 0
    for (let index = 0; index < tokens.length; index += 1) {
      if (!isMarkdownSeparatorCell(tokens[index])) {
        continue
      }
      let end = index
      for (; end < tokens.length && isMarkdownSeparatorCell(tokens[end]); end += 1) {
        // count contiguous separator cells
      }
      if (end-index >= 2) {
        sepStart = index
        sepCount = end-index
        break
      }
    }

    if (sepStart < 0 || sepCount < 2 || sepStart < sepCount) {
      return line
    }

    const headers = tokens.slice(sepStart-sepCount, sepStart)
    if (headers.length !== sepCount || !headers.every((cell) => looksLikePseudoTableHeader(cell) || cell.length <= 20)) {
      return line
    }

    const values = tokens.slice(sepStart + sepCount)
    if (values.length < sepCount) {
      return line
    }

    const rows: string[][] = []
    let cursor = 0
    for (; cursor + sepCount <= values.length; cursor += sepCount) {
      rows.push(values.slice(cursor, cursor + sepCount))
    }
    if (rows.length === 0) {
      return line
    }

    const rebuilt: string[] = []
    rebuilt.push(`| ${headers.join(' | ')} |`)
    rebuilt.push(`| ${headers.map(() => '---').join(' | ')} |`)
    rows.forEach((row) => {
      rebuilt.push(`| ${row.join(' | ')} |`)
    })
    if (cursor < values.length) {
      rebuilt.push('')
      rebuilt.push(values.slice(cursor).join(' '))
    }

    return rebuilt.join('\n')
  })

  return normalized.join('\n')
}

function splitDenseTableCells(line: string): string[] {
  return line
    .split('|')
    .map((cell) => cell.trim())
    .filter(Boolean)
}

function expandDenseDoublePipeTables(content: string): string {
  const lines = content.split('\n')
  const normalized = lines.map((line) => {
    const trimmed = line.trim()
    const doublePipeCount = (trimmed.match(/\|\|+/g) ?? []).length
    const pipeCount = (trimmed.match(/\|/g) ?? []).length

    if (doublePipeCount < 2 || pipeCount < 8) {
      return line
    }

    let rebuilt = trimmed.replace(/\|\|+/g, '\n')
    rebuilt = rebuilt.replace(/\n(>.*)$/g, '\n\n$1')
    rebuilt = rebuilt.replace(/\|\*\*说明[:：]\*\*/g, '\n\n**说明：**')
    rebuilt = rebuilt.replace(/\|说明[:：]/g, '\n\n说明：')
    return rebuilt
  })

  return normalized.join('\n')
}

function normalizeSingleLineDenseTables(content: string): string {
  const lines = content.split('\n')
  const normalized = lines.map((line) => {
    const trimmed = line.trim()
    if (!trimmed.includes('|')) {
      return line
    }

    let tailNote = ''
    let tableSource = trimmed
    const quoteIndex = tableSource.search(/\|\s*>/)
    if (quoteIndex >= 0) {
      tailNote = tableSource.slice(quoteIndex + 1).trim()
      tableSource = tableSource.slice(0, quoteIndex).trim()
    }

    const rowSegments = tableSource
      .split(/\|\|+/)
      .map((segment) => splitDenseTableCells(segment))
      .filter((segment) => segment.length > 0)

    if (rowSegments.length >= 2 && rowSegments[0].length >= 2) {
      const headers = rowSegments[0]
      const separatorLike = rowSegments[1] ?? []
      const hasExplicitSeparator =
        separatorLike.length === headers.length && separatorLike.every((cell) => isMarkdownSeparatorCell(cell))
      const bodyRows = (hasExplicitSeparator ? rowSegments.slice(2) : rowSegments.slice(1)).filter(
        (segment) => segment.length === headers.length,
      )
      const remainderSegments = hasExplicitSeparator ? rowSegments.slice(2 + bodyRows.length) : rowSegments.slice(1 + bodyRows.length)

      if (
        bodyRows.length > 0 &&
        headers.every((cell) => looksLikePseudoTableHeader(cell) || cell.length <= 24)
      ) {
        const rebuilt = [
          `| ${headers.join(' | ')} |`,
          `| ${headers.map(() => '---').join(' | ')} |`,
          ...bodyRows.map((row) => `| ${row.join(' | ')} |`),
        ]
        const remainderText = remainderSegments.flat().join(' ').trim()
        const finalNote = tailNote || remainderText
        if (finalNote) {
          rebuilt.push('')
          rebuilt.push(finalNote.startsWith('>') ? finalNote : `> ${finalNote}`)
        }
        return `\n${rebuilt.join('\n')}\n`
      }
    }

    const parts = splitDenseTableCells(tableSource)
    if (parts.length < 4) {
      return line
    }

    if (containsDenseTableSeparator(tableSource)) {
      let separatorIndex = -1
      for (let i = 0; i < parts.length; i += 1) {
        if (isMarkdownSeparatorCell(parts[i])) {
          separatorIndex = i
          break
        }
      }
      if (separatorIndex <= 1) {
        return line
      }

      let separatorEnd = separatorIndex
      for (; separatorEnd < parts.length && isMarkdownSeparatorCell(parts[separatorEnd]); separatorEnd += 1) {
        // consume contiguous separator cells
      }

      const columnCount = separatorEnd - separatorIndex
      const headers = parts.slice(separatorIndex - columnCount, separatorIndex)
      if (columnCount < 2 || headers.length !== columnCount) {
        return line
      }
      if (!headers.every((cell) => looksLikePseudoTableHeader(cell) || cell.length <= 24)) {
        return line
      }

      const values = parts.slice(separatorEnd)
      if (values.length < columnCount) {
        return line
      }

      const rows: string[][] = []
      let cursor = 0
      while (cursor + columnCount <= values.length) {
        rows.push(values.slice(cursor, cursor + columnCount))
        cursor += columnCount
      }
      if (rows.length === 0) {
        return line
      }

      const rebuilt = [
        `| ${headers.join(' | ')} |`,
        `| ${headers.map(() => '---').join(' | ')} |`,
        ...rows.map((row) => `| ${row.join(' | ')} |`),
      ]

      if (cursor < values.length) {
        rebuilt.push('')
        rebuilt.push(values.slice(cursor).join(' '))
      }
      if (tailNote) {
        rebuilt.push('')
        rebuilt.push(tailNote.startsWith('>') ? tailNote : `> ${tailNote}`)
      }

      return `\n${rebuilt.join('\n')}\n`
    }

    const looksLikePairwiseTable =
      parts.length >= 6 &&
      parts.length % 2 === 0 &&
      parts.every((cell, index) =>
        index % 2 === 0 ? looksLikePseudoTableHeader(cell) || cell.length <= 16 : cell.length > 0,
      )

    if (!looksLikePairwiseTable) {
      return line
    }

    const rebuilt = ['| 维度 | 数据说明 |', '| --- | --- |']
    for (let index = 0; index + 1 < parts.length; index += 2) {
      rebuilt.push(`| ${parts[index]} | ${parts[index + 1]} |`)
    }

    if (tailNote) {
      rebuilt.push('')
      rebuilt.push(tailNote.startsWith('>') ? tailNote : `> ${tailNote}`)
    }

    return `\n${rebuilt.join('\n')}\n`
  })

  return normalized.join('\n')
}

function shouldStopFragmentedTableBlock(line: string): boolean {
  const trimmed = line.trim()
  if (!trimmed) {
    return true
  }
  return /^(##|###|####|>|- |\d+\.|```|文件：|第\d+行：)/.test(trimmed)
}

function normalizeFragmentedPipeTableBlocks(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const current = lines[index].trim()
    const next = lines[index + 1]?.trim() ?? ''
    const currentHasPipe = current.includes('|')
    const nextHasPipe = next.includes('|')

    if ((!currentHasPipe && !nextHasPipe) || shouldStopFragmentedTableBlock(current)) {
      normalized.push(lines[index])
      continue
    }

    const blockLines: string[] = []
    let cursor = index
    while (cursor < lines.length) {
      const raw = lines[cursor]
      const trimmed = raw.trim()
      if (!trimmed) {
        break
      }
      if (cursor > index && shouldStopFragmentedTableBlock(trimmed)) {
        break
      }
      blockLines.push(trimmed)
      cursor += 1
      if (cursor < lines.length && shouldStopFragmentedTableBlock(lines[cursor])) {
        break
      }
    }

    const splitRows = blockLines
      .map((line) => line.split('|').map((cell) => cell.trim()).filter(Boolean))
      .filter((cells) => cells.length > 0)
    const expectedCols = Math.max(...splitRows.map((cells) => cells.length), 0)
    const flattened = splitRows.flat()

    if (expectedCols < 2 || expectedCols > 5 || flattened.length < expectedCols*2) {
      normalized.push(lines[index])
      continue
    }

    const rows: string[][] = []
    for (let start = 0; start + expectedCols <= flattened.length; start += expectedCols) {
      rows.push(flattened.slice(start, start + expectedCols))
    }

    if (rows.length < 2 || !rows[0].every((cell) => looksLikePseudoTableHeader(cell) || cell.length <= 24)) {
      normalized.push(lines[index])
      continue
    }

    normalized.push(`| ${rows[0].join(' | ')} |`)
    normalized.push(`| ${rows[0].map(() => '---').join(' | ')} |`)
    rows.slice(1).forEach((row) => {
      normalized.push(`| ${row.join(' | ')} |`)
    })
    index = cursor - 1
  }

  return normalized.join('\n')
}

function isLikelyTableValueLine(line: string): boolean {
  const trimmed = line.trim()
  if (!trimmed) {
    return false
  }
  if (/^(##|###|>|- |\d+\.|文件：|第\d+行：)/.test(trimmed)) {
    return false
  }
  return !trimmed.includes('|')
}

function normalizeVerticalKeyValueTableBlocks(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const current = lines[index].trim()
    const cells = current.split('|').map((cell) => cell.trim()).filter(Boolean)

    if (cells.length < 3) {
      normalized.push(lines[index])
      continue
    }

    const headers = cells.slice(0, 2)
    const firstRowFirstCell = cells[2]
    const firstRowSecondCell = lines[index + 1]?.trim() ?? ''

    if (!headers.every((cell) => looksLikePseudoTableHeader(cell)) || !isLikelyTableValueLine(firstRowSecondCell)) {
      normalized.push(lines[index])
      continue
    }

    const rows: string[][] = [[firstRowFirstCell, firstRowSecondCell]]
    let cursor = index + 2

    while (cursor + 1 < lines.length) {
      const left = lines[cursor].trim()
      const right = lines[cursor + 1].trim()
      if (!isLikelyTableValueLine(left) || !isLikelyTableValueLine(right)) {
        break
      }
      rows.push([left, right])
      cursor += 2
    }

    if (rows.length < 2) {
      normalized.push(lines[index])
      continue
    }

    normalized.push(`| ${headers.join(' | ')} |`)
    normalized.push(`| ${headers.map(() => '---').join(' | ')} |`)
    rows.forEach((row) => {
      normalized.push(`| ${row.join(' | ')} |`)
    })
    index = cursor - 1
  }

  return normalized.join('\n')
}

type StructuredSpreadsheetBlock = {
  fileName: string
  sheetName?: string
  headers: string[]
  rows: string[][]
}

function parseStructuredSpreadsheetSummary(line: string): StructuredSpreadsheetBlock | null {
  const match = line.match(/^文件：(.+?)。(?:工作表：(.+?)。)?字段：(.+?)。数据行数：(\d+)。?$/)
  if (!match) {
    return null
  }

  const [, fileName, sheetName, headerText] = match
  const headers = headerText
    .split('、')
    .map((item) => item.trim())
    .filter(Boolean)
  if (headers.length === 0) {
    return null
  }

  return {
    fileName: fileName.trim(),
    sheetName: sheetName?.trim(),
    headers,
    rows: [],
  }
}

function parseStructuredSpreadsheetRow(line: string, headers: string[]): string[] | null {
  const match = line.match(/^第\d+行：(.*)$/)
  if (!match) {
    return null
  }

  let payload = match[1].trim()
  payload = payload.replace(/^工作表：[^；]+；/, '')
  if (!payload) {
    return null
  }

  const pairs = [...payload.matchAll(/([^：。；]+)：([^。；]*)/g)]
  if (pairs.length === 0) {
    return null
  }

  const rowMap = new Map<string, string>()
  pairs.forEach(([, key, value]) => {
    rowMap.set(key.trim(), value.trim())
  })

  return headers.map((header) => rowMap.get(header) ?? '')
}

function buildStructuredSpreadsheetMarkdown(block: StructuredSpreadsheetBlock): string {
  const lines: string[] = []
  lines.push(`<div class="md-spreadsheet-note">数据来源：${block.fileName}${block.sheetName ? ` / ${block.sheetName}` : ''}</div>`)
  lines.push('')
  lines.push(`| ${block.headers.join(' | ')} |`)
  lines.push(`| ${block.headers.map(() => '---').join(' | ')} |`)
  block.rows.forEach((row) => {
    lines.push(`| ${row.map((cell) => cell || '—').join(' | ')} |`)
  })
  return lines.join('\n')
}

function normalizeStructuredSpreadsheetBlocks(content: string): string {
  const lines = content.split('\n')
  const normalized: string[] = []

  for (let index = 0; index < lines.length; index += 1) {
    const summaryLine = lines[index].trim()
    const block = parseStructuredSpreadsheetSummary(summaryLine)
    if (!block) {
      normalized.push(lines[index])
      continue
    }

    let cursor = index + 1
    while (cursor < lines.length) {
      const rowLine = lines[cursor].trim()
      if (!rowLine) {
        cursor += 1
        continue
      }
      const row = parseStructuredSpreadsheetRow(rowLine, block.headers)
      if (!row) {
        break
      }
      block.rows.push(row)
      cursor += 1
    }

    if (block.rows.length > 0) {
      normalized.push(buildStructuredSpreadsheetMarkdown(block))
      index = cursor - 1
      continue
    }

    normalized.push(lines[index])
  }

  return normalized.join('\n')
}

type AdviceCardItem = {
  category?: string
  summary?: string
  recommendation?: string
  scenario?: string
  pros?: string
  cons?: string
}

function parseAdviceCardItems(content: string): AdviceCardItem[] {
  return content
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const item: AdviceCardItem = {}
      line.split('|').forEach((part) => {
        const [rawLabel, ...rest] = part.split(/[:：]/)
        if (!rawLabel || rest.length === 0) {
          return
        }
        const key = findSuggestionFieldKey(rawLabel.trim())
        if (!key) {
          return
        }
        item[key as keyof AdviceCardItem] = rest.join('：').trim()
      })
      return item
    })
    .filter((item) => Object.keys(item).length >= 2)
}

const AdviceCardBlock: React.FC<{ content: string }> = ({ content }) => {
  const items = useMemo(() => parseAdviceCardItems(content), [content])

  if (items.length === 0) {
    return (
      <pre className="md-code-block">
        <code>{content}</code>
      </pre>
    )
  }

  return (
    <div className="md-advice-grid">
      {items.map((item, index) => (
        <section key={`${item.category ?? 'item'}-${index}`} className="md-advice-card">
          <div className="md-advice-card-header">
            <span className="md-advice-card-badge">建议分类</span>
            <h4>{item.category ?? `方案 ${index + 1}`}</h4>
          </div>
          <div className="md-advice-card-body">
            {item.summary && (
              <div className="md-advice-row">
                <span className="md-advice-label">说明</span>
                <div className="md-advice-value">{item.summary}</div>
              </div>
            )}
            {item.recommendation && (
              <div className="md-advice-row md-advice-row-emphasis">
                <span className="md-advice-label">建议</span>
                <div className="md-advice-value">{item.recommendation}</div>
              </div>
            )}
            {item.scenario && (
              <div className="md-advice-row">
                <span className="md-advice-label">适用场景</span>
                <div className="md-advice-value">{item.scenario}</div>
              </div>
            )}
            {item.pros && (
              <div className="md-advice-row">
                <span className="md-advice-label">优点</span>
                <div className="md-advice-value">{item.pros}</div>
              </div>
            )}
            {item.cons && (
              <div className="md-advice-row md-advice-row-warning">
                <span className="md-advice-label">注意事项</span>
                <div className="md-advice-value">{item.cons}</div>
              </div>
            )}
          </div>
        </section>
      ))}
    </div>
  )
}

function fixMarkdown(content: string): string {
  let fixed = content.replace(/\r\n/g, '\n').replace(/\r/g, '\n')

  fixed = fixed.replace(/<br\s*\/?>/gi, '\n')
  fixed = fixed.replace(/<\|im_start\|>.*?(?=\n|$)/g, '')
  fixed = fixed.replace(/<\|im_end\|>/g, '')
  fixed = fixed.replace(/<\|endoftext\|>/g, '')
  fixed = fixed.replace(/\|endoftext\|>/g, '')
  fixed = fixed.replace(/<\/?think>/g, '')
  fixed = fixed.replace(/<think>[\s\S]*?<\/think>/g, '')
  fixed = fixed.replace(/<\/?assistant>/g, '')
  fixed = fixed.replace(/<\/?user>/g, '')
  fixed = fixed.replace(/<\/?system>/g, '')

  fixed = fixed.replace(/([^\n])(#{1,6})/g, '$1\n\n$2')
  fixed = fixed.replace(/^(#{1,6})([^\s#])/gm, '$1 $2')
  fixed = fixed.replace(/(#{1,6}\s[^\n|]+)(\|(?=[^\n|]+\|[^\n|]+\|))/g, '$1\n\n$2')
  fixed = fixed.replace(/([^\n])((?:\|[^\n|]+){4,}\|)/g, '$1\n\n$2')
  fixed = fixed.replace(/(\|[^\n]+\|)\s*(>)/g, '$1\n\n$2')

  fixed = expandDenseDoublePipeTables(fixed)
  fixed = normalizeMermaidSection(fixed)
  fixed = normalizeStepSections(fixed)
  fixed = normalizeSummarySections(fixed)
  fixed = normalizeVisualSeparators(fixed)
  fixed = normalizeHeadingAttachedTable(fixed)
  fixed = normalizeSingleLineDenseTables(fixed)
  fixed = normalizeInlineMarkdownTables(fixed)
  fixed = normalizeStructuredSpreadsheetBlocks(fixed)
  fixed = normalizeSuggestionCardBlocks(fixed)
  fixed = normalizeVerticalKeyValueTableBlocks(fixed)
  fixed = normalizeFragmentedPipeTableBlocks(fixed)
  fixed = normalizeLooseTableBlocks(fixed)
  fixed = normalizePainSolutionSection(fixed)
  fixed = normalizeDeliverableSection(fixed)

  fixed = fixed.replace(/\|\s*[-:]+[-| :]*\|/g, (match) => `\n${match}\n`)
  fixed = fixed.replace(/([^\n])(\|[^\n]+\|)/g, '$1\n$2')
  fixed = fixed.replace(/(\|[^\n]+\|)([^\n])/g, '$1\n$2')

  fixed = fixed.replace(
    /(^|\n)((?:[^\n]*[├└│].*(?:\n|$))+)/g,
    (_, prefix, treeBlock: string) => `${prefix}\n\n\
${treeBlock.trimEnd()}\n\
`,
  )

  fixed = fixed.replace(/([^\n])\s+(\d+[.)、]\s*)/g, '$1\n$2')
  fixed = fixed.replace(/([^\n])\s+(第[一二三四五六七八九十]+阶段[:：])/g, '$1\n\n$2')
  fixed = fixed.replace(/([^\n])\s+([-*+])\s+/g, '$1\n$2 ')
  fixed = fixed.replace(/([^\n])\s+-\s+(?=[^\n：:]+[：:])/g, '$1\n- ')

  fixed = fixed.replace(/[✅☑️✔🟩🟦🔹🔸•📌✨📍🛠️📦🚀🎯💡🔥⭐👉🔧📝📣⚠️❗❓]/g, '')
  fixed = fixed.replace(/-\s*(\d+[.)、])/g, '$1')
  fixed = fixed.replace(/([^\n])\s+(总结|结论|建议|风险|下一步|关键任务|阶段功能目标|理由|备注|关键依赖)[:：]/g, '$1\n\n$2：')
  fixed = fixed.replace(/\s+(---|———+|───+)\s+/g, '\n\n---\n\n')
  fixed = fixed.replace(/([^\n])\s*(---)\s*([\u4e00-\u9fa5A-Za-z0-9])/g, '$1\n\n$2\n\n$3')

  fixed = fixed.replace(/([a-z])([A-Z])/g, '$1 $2')
  fixed = fixed.replace(/([a-zA-Z])([\u4e00-\u9fa5])/g, '$1 $2')
  fixed = fixed.replace(/([\u4e00-\u9fa5])([A-Za-z][a-z])/g, '$1 $2')
  fixed = fixed.replace(/([.!?])([A-Za-z\u4e00-\u9fa5])/g, '$1 $2')

  fixed = fixed.replace(/^[ \t]*:[-]{3,}[ \t]*$/gm, '---')
  fixed = fixed.replace(/^[ \t]*\|?[ :-]{3,}\|?[ \t]*$/gm, '---')
  fixed = fixed
    .split('\n')
    .map((line) => normalizePseudoStructuredLine(line))
    .filter(Boolean)
    .join('\n')
  fixed = renumberOrderedListBlocks(fixed)
  fixed = fixed.replace(/([：:])\s*[-*]\s+/g, '$1 ')
  fixed = fixed.replace(/\n{3,}/g, '\n\n')
  fixed = fixed.replace(/[ \t]+\n/g, '\n')
  return fixed.trim()
}

interface MermaidDiagramProps {
  chart: string
}

const MermaidDiagram: React.FC<MermaidDiagramProps> = ({ chart }) => {
  const [svg, setSvg] = useState<string>('')
  const [error, setError] = useState<string>('')
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setSvg('')
    setError('')
    setIsLoading(true)

    const renderChart = async () => {
      try {
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'loose',
          theme: 'default',
        })
        const id = `mermaid-${Math.random().toString(36).slice(2, 10)}`
        const { svg: renderedSvg } = await mermaid.render(id, chart)

        const hasSvgContent = Boolean(renderedSvg && renderedSvg.includes('<svg'))
        const hasSyntaxError = /Syntax error in text|Parse error|Lexical error/i.test(renderedSvg)
        if (!hasSvgContent || hasSyntaxError) {
          throw new Error('invalid mermaid svg')
        }

        if (!cancelled) {
          setSvg(renderedSvg)
          setError('')
          setIsLoading(false)
        }
      } catch {
        if (!cancelled) {
          setSvg('')
          setError('流程图渲染失败，已降级显示源码')
          setIsLoading(false)
        }
      }
    }

    void renderChart()

    const timeout = window.setTimeout(() => {
      if (!cancelled) {
        setSvg('')
        setError('流程图渲染超时，已降级显示源码')
        setIsLoading(false)
      }
    }, 2500)

    return () => {
      cancelled = true
      window.clearTimeout(timeout)
    }
  }, [chart])

  if (error) {
    return (
      <div className="md-mermaid-fallback">
        <div className="md-mermaid-error">{error}</div>
        <pre className="md-code-block">
          <code>{chart}</code>
        </pre>
      </div>
    )
  }

  if (isLoading) {
    return <div className="md-mermaid-loading">流程图渲染中...</div>
  }

  if (!svg) {
    return (
      <div className="md-mermaid-fallback">
        <div className="md-mermaid-error">流程图无有效输出，已降级显示源码</div>
        <pre className="md-code-block">
          <code>{chart}</code>
        </pre>
      </div>
    )
  }

  return <div className="md-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />
}

interface ChatAreaProps {
  sidebarOpen: boolean
  activeConversation: Conversation
  selectedKnowledgeBase: KnowledgeBase | null
  selectedDocument: DocumentItem | null
  config: AppConfig
  isLoading: boolean
  isGlobalGenerating: boolean
  generatingConversationTitle: string
  enforceSingleFlight: boolean
  onSendMessage: (content: string) => Promise<void>
  onClearConversation: () => void
}

const suggestedPrompts = [
  '请总结当前知识库的核心观点',
  '请列出这个知识库中最关键的结论',
  '如果基于当前资料开始实现，下一步建议是什么？',
]

const formatTime = (value: string) =>
  new Date(value).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
  })

const ChatArea: React.FC<ChatAreaProps> = ({
  sidebarOpen,
  activeConversation,
  selectedKnowledgeBase,
  selectedDocument,
  config,
  isLoading,
  isGlobalGenerating,
  generatingConversationTitle,
  enforceSingleFlight,
  onSendMessage,
  onClearConversation,
}) => {
  const [inputValue, setInputValue] = useState('')
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null)
  const messagesEndRef = useRef<HTMLDivElement | null>(null)

  const canSend = inputValue.trim().length > 0 && !(enforceSingleFlight && isGlobalGenerating)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [activeConversation.messages, isLoading])

  const conversationStats = useMemo(() => {
    const userCount = activeConversation.messages.filter(
      (message) => message.role === 'user',
    ).length

    return {
      userCount,
      totalCount: activeConversation.messages.length,
    }
  }, [activeConversation.messages])

  const scopeText = selectedDocument
    ? `文档问答：${selectedDocument.name}`
    : selectedKnowledgeBase
      ? `知识库问答：${selectedKnowledgeBase.name}`
      : '未选择知识库'

  const toolbarItems = [
    {
      icon: '📚',
      text: scopeText,
    },
    {
      icon: '🤖',
      text: config.chat.model,
    },
    {
      icon: '💬',
      text: `${conversationStats.totalCount} 条消息`,
    },
  ]

  const handleSubmit = async () => {
    const content = inputValue.trim()
    if (!content || isLoading) {
      return
    }

    setInputValue('')
    await onSendMessage(content)
  }

  const handleKeyDown = async (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      await handleSubmit()
    }
  }

  const handleCopyMessage = async (messageId: string, content: string) => {
    try {
      await navigator.clipboard.writeText(content)
      setCopiedMessageId(messageId)
      window.setTimeout(() => {
        setCopiedMessageId((prev) => (prev === messageId ? null : prev))
      }, 1500)
    } catch {
      // 忽略复制异常，避免影响主流程
    }
  }

  return (
    <main className={`chat-area ${sidebarOpen ? 'sidebar-open' : 'sidebar-closed'}`}>
      <div className="chat-topbar">
        <div className="chat-topbar-left">
          <span className="chat-topbar-title">AI Assistant</span>
          <span className="chat-topbar-sep">·</span>
          <span className="chat-topbar-hint">{activeConversation.title}</span>
          <span className="chat-topbar-sep">·</span>
          <span className="chat-topbar-hint">{formatTime(activeConversation.updatedAt)}</span>
        </div>

        <div className="chat-topbar-pills">
          {toolbarItems.map((item) => (
            <div key={item.text} className="topbar-pill" title={item.text}>
              <span className="topbar-pill-icon">{item.icon}</span>
              <span className="topbar-pill-text">{item.text}</span>
            </div>
          ))}
        </div>

        <div className="chat-topbar-right">
          {enforceSingleFlight && isGlobalGenerating && (
            <span className="chat-topbar-hint" aria-live="polite">
              正在后台生成：{generatingConversationTitle}
            </span>
          )}
          <button
            type="button"
            className="chat-clear-btn"
            onClick={onClearConversation}
            disabled={isLoading}
          >
            清空对话
          </button>
        </div>
      </div>

      <div className="messages-container">
        {activeConversation.messages.length === 0 ? (
          <div className="welcome-message">
            <h2>欢迎使用 AI LocalBase</h2>
            <p>先选择知识库，或者指定知识库中的单个文档后再进行问答</p>
          </div>
        ) : (
          activeConversation.messages.map((message) => {
            const isStreamingPlaceholder =
              isLoading &&
              message.role === 'assistant' &&
              message.id === activeConversation.messages.at(-1)?.id &&
              !message.content.trim()
            const degradedMetadata =
              message.role === 'assistant' && message.metadata?.degraded
                ? message.metadata
                : null

            return (
              <div key={message.id} className={`message ${message.role}`}>
                {!isStreamingPlaceholder && message.content.trim() && (
                  <button
                    type="button"
                    className="message-copy-btn"
                    onClick={() => {
                      void handleCopyMessage(message.id, message.content)
                    }}
                    aria-label="复制消息"
                    title={copiedMessageId === message.id ? '已复制' : '复制消息'}
                  >
                    {copiedMessageId === message.id ? '✓' : '⧉'}
                  </button>
                )}
                <div
                  className={`message-content ${
                    isStreamingPlaceholder ? 'message-content-thinking' : ''
                  } ${message.role === 'assistant' ? 'message-content-markdown' : ''}`}
                >
                  {degradedMetadata && (
                    <div className="message-degraded-banner" role="status" aria-live="polite">
                      <div className="message-degraded-title">
                        ⚠ 当前回答为降级回复，模型或检索链路出现异常
                      </div>
                      {degradedMetadata.fallbackStrategy && (
                        <div className="message-degraded-detail">
                          策略：{degradedMetadata.fallbackStrategy}
                        </div>
                      )}
                      {degradedMetadata.upstreamError && (
                        <div className="message-degraded-subtle">
                          上游错误：{degradedMetadata.upstreamError}
                        </div>
                      )}
                    </div>
                  )}
                  {isStreamingPlaceholder ? (
                    <div className="thinking-indicator" aria-label="AI 正在思考">
                      <span className="thinking-dot" />
                      <span className="thinking-dot" />
                      <span className="thinking-dot" />
                    </div>
                  ) : message.role === 'assistant' ? (
                    <ReactMarkdown
                      remarkPlugins={[remarkGfm]}
                      components={{
                        code({ className, children, ...props }) {
                          const isInline = !className
                          const codeContent = String(children).replace(/\n$/, '')

                          if (!isInline && className?.includes('language-mermaid')) {
                            return <MermaidDiagram chart={codeContent} />
                          }

                          if (!isInline && className?.includes('language-advice-cards')) {
                            return <AdviceCardBlock content={codeContent} />
                          }

                          return isInline ? (
                            <code className="md-inline-code" {...props}>
                              {children}
                            </code>
                          ) : (
                            <pre className="md-code-block">
                              <code className={className} {...props}>
                                {children}
                              </code>
                            </pre>
                          )
                        },
                        a({ href, children, ...props }) {
                          return (
                            <a
                              href={href}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="md-link"
                              {...props}
                            >
                              {children}
                            </a>
                          )
                        },
                        table({ children, ...props }) {
                          return (
                            <div className="md-table-wrap">
                              <table className="md-data-table" {...props}>
                                {children}
                              </table>
                            </div>
                          )
                        },
                        th({ children, ...props }) {
                          return (
                            <th className="md-data-table-head" {...props}>
                              {children}
                            </th>
                          )
                        },
                        td({ children, ...props }) {
                          const text = React.Children.toArray(children)
                            .map((child) => (typeof child === 'string' ? child : ''))
                            .join('')
                            .trim()
                          const highlight = /(推荐|优先|适合|高|强烈建议|最佳实践|风险|注意)/.test(text)
                          return (
                            <td className={`md-data-table-cell ${highlight ? 'md-data-table-cell-highlight' : ''}`} {...props}>
                              {children}
                            </td>
                          )
                        },
                      }}
                    >
                      {fixMarkdown(message.content)}
                    </ReactMarkdown>
                  ) : (
                    message.content
                  )}
                </div>
                <div className="message-time">{formatTime(message.timestamp)}</div>
              </div>
            )
          })
        )}

        {isLoading && activeConversation.messages.at(-1)?.role !== 'assistant' && (
          <div className="message assistant loading">
            <div className="message-content">AI 正在生成回答...</div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      <div className="prompt-list">
        {suggestedPrompts.map((prompt) => (
          <button
            key={prompt}
            type="button"
            className="prompt-chip"
            disabled={enforceSingleFlight && isGlobalGenerating}
            onClick={() => {
              void onSendMessage(prompt)
            }}
          >
            {prompt}
          </button>
        ))}
      </div>

      <div className="input-area">
        <div className="input-container">
          <textarea
            value={inputValue}
            onChange={(event) => setInputValue(event.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              enforceSingleFlight && isGlobalGenerating
                ? `当前正在后台生成「${generatingConversationTitle}」，请等待完成后再发送`
                : '输入您的问题，Enter 发送，Shift + Enter 换行'
            }
            rows={3}
          />
          <button
            type="button"
            onClick={() => {
              void handleSubmit()
            }}
            disabled={!canSend}
            className="send-btn"
          >
            {isLoading ? '发送中...' : enforceSingleFlight && isGlobalGenerating ? '排队中' : '发送'}
          </button>
        </div>
      </div>
    </main>
  )
}

export default ChatArea
