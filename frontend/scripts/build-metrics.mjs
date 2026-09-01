import fs from 'node:fs'
import path from 'node:path'
import { gzipSync } from 'node:zlib'

const readFiles = (directory) => {
  if (!fs.existsSync(directory)) return []

  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const absolutePath = path.join(directory, entry.name)
    if (entry.isDirectory()) return readFiles(absolutePath)
    return absolutePath.endsWith('.js') ? [absolutePath] : []
  })
}
const assetPathFromReference = (distRoot, fromAsset, reference) => {
  if (reference.startsWith('/')) return path.resolve(distRoot, `.${reference}`)
  if (reference.startsWith('.')) return path.resolve(path.dirname(fromAsset), reference)
  return null
}

const collectStaticImports = (source) => {
  const references = new Set()
  for (const statement of source.split(';')) {
    const staticImport = statement.match(/\bimport\s*(?!\()[\s\S]*?\bfrom\s*["']([^"']+)["']/)
    const sideEffectImport = statement.match(/\bimport\s*["']([^"']+)["']/)
    if (staticImport) references.add(staticImport[1])
    if (sideEffectImport) references.add(sideEffectImport[1])
  }
  return [...references]
}

const chartPattern = /(mermaid|cytoscape|diagram|wardley|flowdiagram|sequence|gantt|architecture)/i

const toAssetMetric = (absolutePath, initial, distRoot) => {
  const content = fs.readFileSync(absolutePath)
  return {
    name: path.basename(absolutePath),
    stableName: path.relative(path.join(distRoot, 'assets'), absolutePath).replaceAll(path.sep, '/'),
    raw: content.length,
    gzip: gzipSync(content, { level: 9 }).length,
    initial,
  }
}

export const stableAssetName = (name) => name.replace(/-[A-Za-z0-9_-]{8,}(?=\.js$)/, '')

export const collectBuildMetrics = ({ projectRoot = process.cwd(), buildDir = 'dist' } = {}) => {
  const distRoot = path.resolve(projectRoot, buildDir)
  const assetsRoot = path.join(distRoot, 'assets')
  const htmlPath = path.join(distRoot, 'index.html')

  if (!fs.existsSync(htmlPath)) {
    throw new Error(`未找到构建入口：${htmlPath}`)
  }

  const html = fs.readFileSync(htmlPath, 'utf8')
  const entryReferences = [...html.matchAll(/<script[^>]+type=["']module["'][^>]+src=["']([^"']+)["']/g)]
    .map((match) => match[1])
    .filter((reference) => reference.endsWith('.js'))
  const preloadReferences = [...html.matchAll(/<link[^>]+rel=["']modulepreload["'][^>]+href=["']([^"']+)["']/g)]
    .map((match) => match[1])
    .filter((reference) => reference.endsWith('.js'))

  if (entryReferences.length === 0) {
    throw new Error('构建入口没有找到模块脚本')
  }

  const visited = new Set()
  const initialAssets = new Set()
  const visitInitialAsset = (assetPath) => {
    if (!assetPath || visited.has(assetPath) || !fs.existsSync(assetPath)) return
    visited.add(assetPath)
    initialAssets.add(assetPath)
    const source = fs.readFileSync(assetPath, 'utf8')
    for (const reference of collectStaticImports(source)) {
      visitInitialAsset(assetPathFromReference(distRoot, assetPath, reference))
    }
  }

  for (const reference of entryReferences) {
    visitInitialAsset(assetPathFromReference(distRoot, htmlPath, reference))
  }
  for (const reference of preloadReferences) {
    visitInitialAsset(assetPathFromReference(distRoot, htmlPath, reference))
  }

  const stats = readFiles(assetsRoot).map((absolutePath) => (
    toAssetMetric(absolutePath, initialAssets.has(absolutePath), distRoot)
  ))
  const entryStats = stats.filter((item) => entryReferences.some((reference) => (
    item.name === path.basename(assetPathFromReference(distRoot, htmlPath, reference) || '')
  )))
  const initialJavaScript = stats.filter((item) => item.initial)
  const asyncChartStats = stats.filter((item) => !item.initial && chartPattern.test(item.name))
  const initialChartAssets = initialJavaScript.filter((item) => chartPattern.test(item.name))

  return {
    stats,
    entryStats,
    initialJavaScript,
    initialChartAssets,
    asyncChartStats,
    metrics: {
      entryGzipBytes: entryStats.reduce((total, item) => total + item.gzip, 0),
      initialJavaScriptRawBytes: initialJavaScript.reduce((total, item) => total + item.raw, 0),
      initialJavaScriptGzipBytes: initialJavaScript.reduce((total, item) => total + item.gzip, 0),
      maxInitialChunkRawBytes: Math.max(0, ...initialJavaScript.map((item) => item.raw)),
      maxAsyncChartRawBytes: Math.max(0, ...asyncChartStats.map((item) => item.raw)),
      maxAsyncChartGzipBytes: Math.max(0, ...asyncChartStats.map((item) => item.gzip)),
      asyncChartChunkCount: asyncChartStats.length,
    },
  }
}
