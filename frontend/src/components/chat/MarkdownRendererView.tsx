import React from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import MarkdownMermaid from './MarkdownMermaid'

interface MarkdownRendererViewProps {
  content: string
  AdviceCardBlock?: React.ComponentType<{ content: string }>
}

// Keep third-party renderer wiring separate from the normalization pipeline.
// This makes parser fixes testable without mounting Mermaid or ReactMarkdown.
const MarkdownRendererView: React.FC<MarkdownRendererViewProps> = ({
  content,
  AdviceCardBlock,
}) => (
  <ReactMarkdown
    remarkPlugins={[remarkGfm]}
    components={{
      code({ className, children, ...props }) {
        const isInline = !className
        const codeContent = String(children).replace(/\n$/, '')

        if (!isInline && className?.includes('language-mermaid')) {
          return <MarkdownMermaid chart={codeContent} />
        }

        if (!isInline && className?.includes('language-advice-cards') && AdviceCardBlock) {
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
      strong({ children, ...props }) {
        const text = React.Children.toArray(children)
          .map((child) => (typeof child === 'string' ? child : ''))
          .join('')
          .trim()
        return (
          <strong
            className={text.length > 48 ? 'md-strong-plain' : undefined}
            {...props}
          >
            {children}
          </strong>
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
          <td
            className={`md-data-table-cell ${highlight ? 'md-data-table-cell-highlight' : ''}`}
            {...props}
          >
            {children}
          </td>
        )
      },
    }}
  >
    {content}
  </ReactMarkdown>
)

export default MarkdownRendererView
