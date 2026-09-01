import React, { type ErrorInfo, type ReactNode } from 'react'

interface MarkdownRenderErrorBoundaryProps {
  content: string
  children: ReactNode
}

interface MarkdownRenderErrorBoundaryState {
  hasError: boolean
}

class MarkdownRenderErrorBoundary extends React.Component<
  MarkdownRenderErrorBoundaryProps,
  MarkdownRenderErrorBoundaryState
> {
  state: MarkdownRenderErrorBoundaryState = { hasError: false }

  static getDerivedStateFromError(): MarkdownRenderErrorBoundaryState {
    return { hasError: true }
  }

  componentDidUpdate(previousProps: MarkdownRenderErrorBoundaryProps) {
    if (previousProps.content !== this.props.content && this.state.hasError) {
      this.setState({ hasError: false })
    }
  }

  componentDidCatch(error: Error, _errorInfo: ErrorInfo) {
    if (import.meta.env.DEV) {
      console.warn('markdown render failed, showing source fallback', error)
    }
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="md-render-fallback" role="status">
          <div className="md-mermaid-error">内容渲染失败，已降级显示原文</div>
          <pre className="md-code-block">
            <code>{this.props.content}</code>
          </pre>
        </div>
      )
    }

    return this.props.children
  }
}

export default MarkdownRenderErrorBoundary
