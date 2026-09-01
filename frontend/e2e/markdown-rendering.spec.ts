import { test, expect, type Page } from '@playwright/test'

const chartAssetPattern = /mermaid|cytoscape|diagram|wardley|flowdiagram|sequence|gantt|architecture/i

const openFixture = async (page: Page, fixture: string) => {
  await page.goto(`/e2e/markdown-fixture.html?case=${fixture}`)
  await expect(page.getByTestId('fixture-ready')).toBeVisible()
}

const trackChartRequests = (page: Page) => {
  const requests: string[] = []
  page.on('request', (request) => {
    if (chartAssetPattern.test(request.url())) {
      requests.push(request.url())
    }
  })
  return requests
}

test.describe('独立 Markdown 渲染 fixture', () => {
  test('普通 Markdown、表格、代码和引用不请求图表依赖', async ({ page }) => {
    const chartRequests = trackChartRequests(page)
    await openFixture(page, 'basic')

    await expect(page.getByRole('heading', { name: '基础渲染' })).toBeVisible()
    await expect(page.locator('blockquote')).toContainText('这是一条可读的引用')
    await expect(page.locator('.md-data-table')).toContainText('fixture')
    await expect(page.locator('.md-code-block')).toContainText('plain source block')
    expect(chartRequests).toEqual([])
  })

  test('常用 Mermaid 流程图按需加载并渲染 SVG', async ({ page }) => {
    const chartRequests = trackChartRequests(page)
    await openFixture(page, 'flowchart')

    await expect(page.locator('.md-mermaid > svg').first()).toBeVisible({ timeout: 30_000 })
    expect(chartRequests.some((url) => /mermaid/i.test(url))).toBeTruthy()
    await expect(page.locator('.md-mermaid-fallback')).toHaveCount(0)
  })

  test('Mermaid 架构图按需加载图表布局依赖', async ({ page }) => {
    const chartRequests = trackChartRequests(page)
    await openFixture(page, 'architecture')

    await expect(page.locator('.md-mermaid > svg').first()).toBeVisible({ timeout: 30_000 })
    // Vite dev mode may bundle Cytoscape into the architecture dependency;
    // the production build budget separately verifies its named async chunks.
    expect(chartRequests.some((url) => /architectureDiagram|architecture-/i.test(url))).toBeTruthy()
  })

  test('语法错误会保留源码并显示失败回退', async ({ page }) => {
    await openFixture(page, 'invalid')

    await expect(page.getByText('流程图渲染失败，已降级显示源码')).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('.md-mermaid-fallback code')).toContainText('intentionally invalid')
  })

  test('动态模块加载失败不会吞掉消息内容', async ({ page }) => {
    let intercepted = false
    await page.route('**/src/components/chat/MarkdownMermaid.tsx*', async (route) => {
      intercepted = true
      await route.abort()
    })
    await openFixture(page, 'load-failure')

    await expect(page.getByText('内容渲染失败，已降级显示原文')).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('.md-render-fallback code')).toContainText('加载')
    expect(intercepted).toBe(true)
  })

  test('同一页面的多个图表不会重复请求同一个动态模块', async ({ page }) => {
    const chartRequests = trackChartRequests(page)
    await openFixture(page, 'multi')

    await expect(page.locator('.md-mermaid svg')).toHaveCount(2, { timeout: 30_000 })
    const moduleRequests = chartRequests.filter((url) => /MarkdownMermaid|mermaid/i.test(url))
    expect(moduleRequests.length).toBeGreaterThan(0)
    expect(new Set(moduleRequests).size).toBe(moduleRequests.length)
  })

  test('窄屏渲染没有横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openFixture(page, 'basic')

    const dimensions = await page.evaluate(() => ({
      bodyScrollWidth: document.body.scrollWidth,
      bodyClientWidth: document.body.clientWidth,
    }))
    expect(dimensions.bodyScrollWidth).toBeLessThanOrEqual(dimensions.bodyClientWidth)
  })
})
