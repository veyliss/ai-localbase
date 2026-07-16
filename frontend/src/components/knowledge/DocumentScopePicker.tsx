import React, { useDeferredValue, useEffect, useId, useMemo, useRef, useState } from 'react'
import type { DocumentItem } from '../../App'
import AppIcon from '../common/AppIcon'
import { DOCUMENT_SCOPE_RESULT_LIMIT, getDocumentScopeMatches } from './documentScopeOptions'

interface DocumentScopePickerProps {
  documents: DocumentItem[]
  selectedDocumentId: string | null
  onSelectDocument: (documentId: string | null) => void
}

const DocumentScopePicker: React.FC<DocumentScopePickerProps> = ({
  documents,
  selectedDocumentId,
  onSelectDocument,
}) => {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const deferredQuery = useDeferredValue(query)
  const pickerId = useId()
  const listboxId = `${pickerId}-listbox`
  const pickerRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const optionsRef = useRef<HTMLDivElement>(null)
  const selectedDocument = useMemo(
    () => documents.find((document) => document.id === selectedDocumentId) ?? null,
    [documents, selectedDocumentId],
  )
  const matches = useMemo(
    () => getDocumentScopeMatches(documents, deferredQuery, selectedDocumentId),
    [deferredQuery, documents, selectedDocumentId],
  )
  const options = useMemo(
    () => [null, ...matches.visible] as Array<DocumentItem | null>,
    [matches.visible],
  )
  const boundedActiveIndex = Math.min(activeIndex, Math.max(options.length - 1, 0))

  useEffect(() => {
    if (!open) return

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && pickerRef.current?.contains(target)) return
      setOpen(false)
      setQuery('')
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      setQuery('')
      triggerRef.current?.focus()
    }

    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    searchRef.current?.focus()
  }, [open])

  useEffect(() => {
    if (!open) return
    const selectedIndex = selectedDocumentId
      ? options.findIndex((document) => document?.id === selectedDocumentId)
      : 0
    if (deferredQuery) {
      setActiveIndex(selectedIndex > 0 ? selectedIndex : (matches.visible.length > 0 ? 1 : 0))
      return
    }
    setActiveIndex(selectedIndex >= 0 ? selectedIndex : 0)
  }, [deferredQuery, matches.visible.length, open, options, selectedDocumentId])

  useEffect(() => {
    if (!open) return
    optionsRef.current
      ?.querySelector<HTMLElement>(`[id="${pickerId}-option-${boundedActiveIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [boundedActiveIndex, open, pickerId])

  const selectOption = (documentId: string | null) => {
    onSelectDocument(documentId)
    setOpen(false)
    setQuery('')
    requestAnimationFrame(() => triggerRef.current?.focus())
  }

  const togglePicker = () => {
    if (open) setQuery('')
    setOpen((current) => !current)
  }

  const handleSearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex((current) => Math.min(current + 1, options.length - 1))
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex((current) => Math.max(current - 1, 0))
    }
    if (event.key === 'Home') {
      event.preventDefault()
      setActiveIndex(0)
    }
    if (event.key === 'End') {
      event.preventDefault()
      setActiveIndex(Math.max(options.length - 1, 0))
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      selectOption(options[boundedActiveIndex]?.id ?? null)
    }
  }

  const activeOptionId = `${pickerId}-option-${boundedActiveIndex}`
  const isLimited = matches.total > matches.visible.length

  return (
    <div className={`kb-document-scope-picker${open ? ' kb-document-scope-picker--open' : ''}`} ref={pickerRef}>
      <span className="kb-document-scope-label">检索范围</span>
      <button
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={`检索范围：${selectedDocument?.name ?? '全部文档'}`}
        className="kb-document-scope-trigger"
        onClick={togglePicker}
        onKeyDown={(event) => {
          if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && !open) {
            event.preventDefault()
            setOpen(true)
          }
        }}
        ref={triggerRef}
        type="button"
      >
        <span>{selectedDocument?.name ?? '全部文档'}</span>
        <AppIcon name={open ? 'chevronUp' : 'chevronDown'} size={14} />
      </button>

      {open && (
        <div aria-label="选择检索范围" className="kb-document-scope-popover" role="dialog">
          <div className="kb-document-scope-search">
            <AppIcon name="search" size={15} />
            <input
              aria-activedescendant={activeOptionId}
              aria-autocomplete="list"
              aria-controls={listboxId}
              aria-expanded="true"
              aria-label="搜索检索范围中的文档"
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={handleSearchKeyDown}
              placeholder="输入文件名搜索"
              ref={searchRef}
              role="combobox"
              type="search"
              value={query}
            />
            {query && (
              <button aria-label="清除文档搜索" onClick={() => setQuery('')} title="清除" type="button">
                <AppIcon name="x" size={14} />
              </button>
            )}
          </div>

          <div className="kb-document-scope-summary">
            <span>{deferredQuery ? `${matches.total} 个匹配` : `${documents.length} 份文档`}</span>
            {isLimited && <span>显示前 {DOCUMENT_SCOPE_RESULT_LIMIT} 项</span>}
          </div>

          <div className="kb-document-scope-options" id={listboxId} ref={optionsRef} role="listbox">
            <button
              aria-selected={!selectedDocumentId}
              className={`${boundedActiveIndex === 0 ? 'is-active ' : ''}${!selectedDocumentId ? 'is-selected' : ''}`}
              id={`${pickerId}-option-0`}
              onClick={() => selectOption(null)}
              onMouseEnter={() => setActiveIndex(0)}
              role="option"
              type="button"
            >
              <span className="kb-document-scope-option-icon"><AppIcon name="book" size={16} /></span>
              <span className="kb-document-scope-option-copy">
                <strong>全部文档</strong>
                <small>在当前知识库的全部资料中检索</small>
              </span>
              {!selectedDocumentId && <AppIcon className="kb-document-scope-check" name="check" size={15} />}
            </button>

            {matches.visible.map((document, index) => {
              const optionIndex = index + 1
              const selected = document.id === selectedDocumentId
              return (
                <button
                  aria-selected={selected}
                  className={`${boundedActiveIndex === optionIndex ? 'is-active ' : ''}${selected ? 'is-selected' : ''}`}
                  id={`${pickerId}-option-${optionIndex}`}
                  key={document.id}
                  onClick={() => selectOption(document.id)}
                  onMouseEnter={() => setActiveIndex(optionIndex)}
                  role="option"
                  type="button"
                >
                  <span className="kb-document-scope-option-icon"><AppIcon name="file" size={16} /></span>
                  <span className="kb-document-scope-option-copy">
                    <strong title={document.name}>{document.name}</strong>
                    <small>{document.sizeLabel} · {document.chunkCount ?? 0} chunks · {new Date(document.uploadedAt).toLocaleDateString('zh-CN')}</small>
                  </span>
                  {selected && <AppIcon className="kb-document-scope-check" name="check" size={15} />}
                </button>
              )
            })}

            {matches.total === 0 && (
              <div className="kb-document-scope-empty">
                <AppIcon name="search" size={18} />
                <span>没有匹配的文件</span>
              </div>
            )}
          </div>

          {isLimited && <p className="kb-document-scope-hint">继续输入更精确的文件名以缩小范围。</p>}
        </div>
      )}
    </div>
  )
}

export default DocumentScopePicker
