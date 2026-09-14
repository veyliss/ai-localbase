import type { KnowledgeBaseHealthResponse } from '../../services/api'

export interface LabelTone {
  text: string
  color: string
  bg: string
}

export const documentStatusLabel = (status: string): LabelTone => {
  if (status === 'indexed') return { text: '已索引', color: 'var(--color-success)', bg: 'var(--color-success-light)' }
  if (status === 'processing') return { text: '处理中', color: 'var(--color-warning)', bg: 'var(--color-warning-light)' }
  if (status === 'failed') return { text: '失败', color: 'var(--color-error)', bg: 'var(--color-error-light)' }
  return { text: '就绪', color: 'var(--color-primary)', bg: 'var(--color-primary-light)' }
}

export const healthStatusLabel = (status: KnowledgeBaseHealthResponse['status']): LabelTone => {
  if (status === 'healthy') return { text: '健康', color: 'var(--color-success)', bg: 'var(--color-success-light)' }
  if (status === 'warning') return { text: '需关注', color: 'var(--color-warning)', bg: 'var(--color-warning-light)' }
  if (status === 'attention') return { text: '需处理', color: 'var(--color-error)', bg: 'var(--color-error-light)' }
  return { text: '空库', color: 'var(--text-secondary)', bg: 'var(--surface-muted)' }
}

export const chunkKindLabel = (kind: string): string => {
  if (kind === 'structured_summary') return '摘要'
  if (kind === 'structured_row') return '数据行'
  return '正文'
}

export const vectorCountLabel = (count: number, source?: string): string => {
  if (source === 'actual') return `${count}`
  if (source === 'estimated') return `约 ${count}`
  if (source === 'not_applicable') return '未启用'
  return '未确认'
}

export const vectorCountSourceLabel = (source?: string): string => {
  if (source === 'actual') return 'Qdrant 实际统计'
  if (source === 'estimated') return '估算值'
  if (source === 'not_applicable') return '未启用 Qdrant'
  return '无法确认'
}

export const qdrantStatusLabel = (status?: string, enabled?: boolean): string => {
  if (!enabled) return '未启用'
  if (status === 'ok') return '连接正常'
  if (status === 'collection_missing') return 'Collection 不存在'
  if (status === 'dimension_mismatch') return '向量维度不一致'
  if (status === 'sparse_vectors_missing') return '缺少稀疏向量配置'
  if (status === 'collection_unhealthy') return 'Collection 状态异常'
  if (status === 'error') return '检查失败'
  return '无法确认'
}
