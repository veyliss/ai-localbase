import { collectBuildMetrics, stableAssetName } from './build-metrics.mjs'

const limits = {
  entryGzip: 100 * 1024,
  initialGzip: 200 * 1024,
  initialChunkRaw: 500 * 1024,
  asyncChunkRaw: 500 * 1024,
}

const asyncChunkExceptions = [
  {
    pattern: /^mermaid\.core-/,
    maxRaw: 650 * 1024,
    reason: 'Mermaid 官方 core 负责按图表类型注册异步 loader，强行拆分会导致首屏预加载或循环块',
  },
  {
    pattern: /^(?:wardley-|mermaid-parser-)/,
    maxRaw: 650 * 1024,
    reason: 'Mermaid 官方 parser 将多个语法服务编译进单一模块，只在对应图表首次出现时加载',
  },
]

const formatBytes = (value) => {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(2)} MB`
}

let build
try {
  build = collectBuildMetrics({
    projectRoot: process.cwd(),
    buildDir: process.env.BUILD_DIR || 'dist',
  })
} catch (error) {
  console.error(`构建预算失败：${error instanceof Error ? error.message : String(error)}`)
  process.exit(1)
}

const { entryStats, initialJavaScript, initialChartAssets, asyncStats, asyncChartStats, metrics } = build
const failures = []
const exceptionStats = []

for (const entry of entryStats) {
  if (entry.gzip > limits.entryGzip) {
    failures.push(`入口 ${entry.name} gzip ${formatBytes(entry.gzip)} > ${formatBytes(limits.entryGzip)}`)
  }
}

if (metrics.initialJavaScriptGzipBytes > limits.initialGzip) {
  failures.push(`首屏 JavaScript gzip ${formatBytes(metrics.initialJavaScriptGzipBytes)} > ${formatBytes(limits.initialGzip)}`)
}
for (const item of initialJavaScript) {
  if (item.raw > limits.initialChunkRaw) {
    failures.push(`首屏块 ${item.name} raw ${formatBytes(item.raw)} > ${formatBytes(limits.initialChunkRaw)}`)
  }
}
for (const item of initialChartAssets) {
  failures.push(`图表依赖误进入首屏：${item.name}`)
}
for (const item of asyncStats) {
  const exception = asyncChunkExceptions.find((candidate) => candidate.pattern.test(item.name))
  if (exception) {
    exceptionStats.push({ item, exception })
    if (item.raw > exception.maxRaw) {
      failures.push(`异步图表例外 ${item.name} raw ${formatBytes(item.raw)} > ${formatBytes(exception.maxRaw)}`)
    }
  } else if (item.raw > limits.asyncChunkRaw) {
    failures.push(`异步块 ${item.name} raw ${formatBytes(item.raw)} > ${formatBytes(limits.asyncChunkRaw)}`)
  }
}

console.log('构建预算检查')
console.log(`- 入口：${entryStats.map((item) => `${item.name} ${formatBytes(item.raw)} / gzip ${formatBytes(item.gzip)}`).join(', ')}`)
console.log(`- 首屏 JavaScript：${formatBytes(metrics.initialJavaScriptGzipBytes)} gzip，${initialJavaScript.length} 个块`)
console.log(`- 全部异步块：${asyncStats.length} 个，最大 ${formatBytes(metrics.maxAsyncChunkRawBytes)}`)
console.log(`- 异步图表块：${asyncChartStats.length} 个，最大 ${formatBytes(metrics.maxAsyncChartRawBytes)}`)
for (const { item, exception } of exceptionStats) {
  console.log(`- 异步例外：${item.name} ${formatBytes(item.raw)}，上限 ${formatBytes(exception.maxRaw)}，原因：${exception.reason}`)
}
if (initialChartAssets.length === 0) {
  console.log('- 首屏未发现 Mermaid/Cytoscape 图表块')
} else {
  console.log(`- 首屏图表块：${initialChartAssets.map((item) => item.name).join(', ')}`)
}
console.log('主要产物：')
for (const item of [...build.stats].sort((left, right) => right.raw - left.raw).slice(0, 12)) {
  console.log(`  ${stableAssetName(item.name)}: ${formatBytes(item.raw)} / gzip ${formatBytes(item.gzip)}${item.initial ? ' / 首屏' : ''}`)
}

if (failures.length > 0) {
  console.error('构建预算未通过：')
  for (const failure of failures) console.error(`- ${failure}`)
  process.exit(1)
}

console.log('构建预算通过。')
