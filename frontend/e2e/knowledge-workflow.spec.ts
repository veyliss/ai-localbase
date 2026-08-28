import { test, expect, type Page, type TestInfo } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const publicFixtureFile = path.join(
  repositoryRoot,
  'backend/eval/fixtures/public-v1/ai-tech-facts.md',
)
const publicFixtureEnabled = process.env.E2E_PUBLIC_FIXTURE === '1'
const publicFixtureQuery = 'Qdrant 的 payload 可以存储什么类型的信息？'
const publicNoAnswerQuery = 'Hugging Face Transformers 官方文档是否提供了每个模型的在线响应时间？'
const externalFileAllowed = process.env.E2E_ALLOW_EXTERNAL_FILE === '1'
const externalUploadFile = process.env.E2E_UPLOAD_FILE
  ? path.resolve(process.env.E2E_UPLOAD_FILE)
  : ''
const uploadFile = publicFixtureEnabled ? publicFixtureFile : externalUploadFile
const retrievalQuery = publicFixtureEnabled
  ? publicFixtureQuery
  : process.env.E2E_QUERY?.trim() || ''
const chatQuery = process.env.E2E_CHAT_QUERY?.trim() || ''
const configuredUsername = process.env.E2E_USERNAME?.trim() || ''
const password = process.env.E2E_PASSWORD || ''
const workflowEnabled = Boolean(
  uploadFile &&
  fs.existsSync(uploadFile) &&
  retrievalQuery &&
  (publicFixtureEnabled || externalFileAllowed),
)

const loginIfRequired = async (page: Page, testInfo: TestInfo) => {
  await page.goto('/')
  await page.waitForLoadState('domcontentloaded')

  await expect(
    page.getByRole('button', { name: /登录工作区|创建账户并进入|知识库/ }).first(),
  ).toBeVisible({ timeout: 30_000 })

  const setupButton = page.getByRole('button', { name: '创建账户并进入' })
  if (await setupButton.isVisible().catch(() => false)) {
    testInfo.skip(true, '请先完成应用首次初始化，再运行浏览器工作流测试')
  }

  const loginButton = page.getByRole('button', { name: '登录工作区' })
  if (!(await loginButton.isVisible().catch(() => false))) {
    return
  }

  if (!password) {
    testInfo.skip(true, '检测到登录保护，请设置 E2E_PASSWORD 后运行浏览器工作流测试')
  }

  const usernameInput = page.getByRole('textbox', { name: '用户名' })
  const defaultUsername = (await usernameInput.inputValue()).trim() || 'root'
  await usernameInput.fill(configuredUsername || defaultUsername)
  await page.getByRole('textbox', { name: '密码' }).fill(password)
  await loginButton.click()

  const workspaceButton = page.getByRole('button', { name: '知识库', exact: true })
  try {
    await expect(workspaceButton).toBeVisible({ timeout: 30_000 })
  } catch {
    const loginError = await page.locator('.login-error').first().textContent().catch(() => '')
    throw new Error(loginError?.trim() || '登录后未进入工作区，请检查 E2E_USERNAME 和 E2E_PASSWORD')
  }
}

const createTemporaryKnowledgeBase = async (page: Page) => {
  const name = `E2E 临时知识库 ${Date.now()}`
  await page.locator('.app-rail-nav').getByRole('button', { name: '知识库', exact: true }).click()
  await page.locator('.kb-header').getByRole('button', { name: '新建知识库', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '新建知识库' })).toBeVisible()
  await page.locator('#kb-name-input').fill(name)
  await page.getByRole('dialog', { name: '新建知识库' }).getByRole('button', { name: '创建知识库' }).click()
  await expect(page.getByRole('heading', { name })).toBeVisible()

  const knowledgeBaseId = await page.evaluate(async (knowledgeBaseName) => {
    const response = await fetch('/api/knowledge-bases', { credentials: 'same-origin' })
    if (!response.ok) return ''
    const payload = await response.json() as { items?: Array<{ id: string; name: string }> }
    return payload.items?.find((item) => item.name === knowledgeBaseName)?.id || ''
  }, name)

  return { name, knowledgeBaseId }
}

const uploadAndWaitForIndex = async (page: Page) => {
  const fileName = path.basename(uploadFile)
  await page.getByRole('button', { name: '上传' }).click()
  await page.getByRole('menuitem', { name: '上传文件' }).click()
  await page.locator('input[type="file"][accept*=".txt"]').setInputFiles(uploadFile)

  await expect(page.getByText(fileName, { exact: true })).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText('已完成', { exact: true })).toBeVisible({ timeout: 120_000 })
  return fileName
}

const cleanupKnowledgeBase = async (page: Page, knowledgeBaseId: string) => {
  if (!knowledgeBaseId) return
  await page.evaluate(async (id) => {
    const csrfCookie = document.cookie
      .split('; ')
      .find((cookie) => cookie.startsWith('ai_localbase_csrf='))
    const csrfToken = csrfCookie ? decodeURIComponent(csrfCookie.split('=').slice(1).join('=')) : ''
    await fetch(`/api/knowledge-bases/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      credentials: 'same-origin',
      headers: csrfToken ? { 'X-CSRF-Token': csrfToken } : undefined,
    })
  }, knowledgeBaseId)
}

test.describe('知识库核心浏览器工作流', () => {
  let knowledgeBaseId = ''

  test.skip(
    !workflowEnabled,
    '请设置 E2E_PUBLIC_FIXTURE=1，或明确设置 E2E_ALLOW_EXTERNAL_FILE=1、E2E_UPLOAD_FILE 和 E2E_QUERY 后运行浏览器工作流测试',
  )

  test.beforeEach(async ({ page }, testInfo) => {
    await loginIfRequired(page, testInfo)
    const created = await createTemporaryKnowledgeBase(page)
    knowledgeBaseId = created.knowledgeBaseId
    expect(knowledgeBaseId, '创建的临时知识库应能通过 API 查询到').toBeTruthy()
  })

  test.afterEach(async ({ page }) => {
    await cleanupKnowledgeBase(page, knowledgeBaseId)
    knowledgeBaseId = ''
  })

  test('上传文件、确认索引、运行检索并打开文档详情', async ({ page }) => {
    const fileName = await uploadAndWaitForIndex(page)

    await page.getByRole('tab', { name: '检索测试' }).click()
    await page.getByRole('textbox', { name: '检索测试问题' }).fill(retrievalQuery)
    await page.getByRole('button', { name: '运行检索' }).click()

    await expect(page.getByText('检索耗时', { exact: true })).toBeVisible({ timeout: 120_000 })
    await expect(page.getByText(/命中结果|没有命中 Chunk/)).toBeVisible()
    await expect(page.getByText('检索失败', { exact: true })).toHaveCount(0)

    if (publicFixtureEnabled) {
      const evidenceHit = page.locator('.kb-retrieval-hit').filter({ hasText: 'Qdrant 的 payload' }).first()
      await expect(evidenceHit).toBeVisible()
      await expect(evidenceHit).toContainText('JSON')
      await expect(evidenceHit).toContainText('任意信息')
    }

    await page.getByRole('tab', { name: '文档' }).click()
    const documentRow = page.locator('.kb-doc-item').filter({ hasText: fileName }).first()
    await expect(documentRow).toBeVisible()
    await documentRow.getByRole('button', { name: `打开 ${fileName} 的操作菜单` }).click()
    await page.getByRole('menuitem', { name: '查看详情' }).click()
    const detailDialog = page.locator('.kb-detail-dialog')
    await expect(detailDialog).toBeVisible()

    if (publicFixtureEnabled) {
      await detailDialog.getByRole('tab', { name: '原文', exact: true }).click()
      await expect(detailDialog.locator('.kb-detail-pre')).toContainText('Qdrant 的 payload')
      await expect(detailDialog.locator('.kb-detail-pre')).toContainText('JSON')

      await detailDialog.getByRole('tab', { name: /^Chunks/ }).click()
      await expect(detailDialog.locator('.kb-detail-chunks')).toContainText('任意信息')
    }
  })

  test('公开资料没有答案时进入低置信路径', async ({ page }) => {
    test.skip(!publicFixtureEnabled, '仅公开 fixture 模式验证无答案保护')

    await uploadAndWaitForIndex(page)
    await page.getByRole('tab', { name: '检索测试' }).click()
    await page.getByRole('textbox', { name: '检索测试问题' }).fill(publicNoAnswerQuery)

    const retrievalResponse = page.waitForResponse((response) => (
      response.url().includes('/retrieval/debug') &&
      response.request().method() === 'POST'
    ))
    await page.getByRole('button', { name: '运行检索' }).click()
    const response = await retrievalResponse
    expect(response.ok()).toBeTruthy()

    const payload = await response.json() as { count?: number; lowConfidence?: boolean }
    expect(payload.lowConfidence || payload.count === 0).toBeTruthy()
    await expect(page.getByText('需要复核', { exact: true })).toBeVisible({ timeout: 120_000 })
    await expect(page.getByText('检索失败', { exact: true })).toHaveCount(0)
  })

  test('在配置了问答模型时，引用可以打开文档定位', async ({ page }) => {
    test.skip(!chatQuery, '设置 E2E_CHAT_QUERY 后运行聊天引用工作流测试')

    const fileName = await uploadAndWaitForIndex(page)
    await page.getByRole('button', { name: '聊天' }).click()
    await page.locator('textarea[placeholder*="输入您的问题"]').fill(chatQuery)
    await page.getByRole('button', { name: '发送消息' }).click()

    await expect(page.locator('.messages-container')).toContainText(chatQuery, { timeout: 30_000 })
    await expect(page.getByText('引用来源', { exact: true })).toBeVisible({ timeout: 120_000 })
    await page.getByRole('button', { name: '定位' }).first().click()
    await expect(page.getByRole('dialog', { name: '引用来源详情' })).toBeVisible()
    await page.getByRole('button', { name: '跳转到文档详情' }).click()
    await expect(page.getByRole('dialog', { name: '文档详情' })).toContainText(fileName)
  })
})
