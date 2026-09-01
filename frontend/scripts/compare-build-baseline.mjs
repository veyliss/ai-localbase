import fs from 'node:fs'
import path from 'node:path'
import { collectBuildMetrics, stableAssetName } from './build-metrics.mjs'

const baselinePath = path.resolve(
  process.cwd(),
  process.env.BUILD_BASELINE_FILE || 'scripts/build-baseline.json',
)
const shouldWrite = process.argv.includes('--write')
const formatBytes = (value) => {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / (1024 * 1024)).toFixed(2)} MB`
}
const formatChange = (current, baseline) => {
  if (baseline === 0) return current === 0 ? '0%' : '新增'
  const change = ((current - baseline) / baseline) * 100
  return `${change >= 0 ? '+' : ''}${change.toFixed(1)}%`
}

let current
try {
  current = collectBuildMetrics({
    projectRoot: process.cwd(),
    buildDir: process.env.BUILD_DIR || 'dist',
  })
} catch (error) {
  console.error(`构建基线失败：${error instanceof Error ? error.message : String(error)}`)
  process.exit(1)
}

const currentBaseline = {
  schemaVersion: 1,
  baselineName: process.env.BUILD_BASELINE_NAME || 'v1.5-20260901',
  capturedAt: new Date().toISOString(),
  metrics: current.metrics,
  initialChartAssets: current.initialChartAssets.map((item) => stableAssetName(item.name)),
  largestAssets: [...current.stats]
    .sort((left, right) => right.raw - left.raw)
    .slice(0, 12)
    .map((item) => ({
      name: stableAssetName(item.name),
      rawBytes: item.raw,
      gzipBytes: item.gzip,
      initial: item.initial,
    })),
  notes: [
    '只记录构建产物大小、稳定化模块名和首屏标志，不记录用户数据或运行内容。',
    '基线比较用于发现回归，绝对上限由 check-build-budget.mjs 单独执行。',
  ],
}

if (shouldWrite) {
  fs.mkdirSync(path.dirname(baselinePath), { recursive: true })
  fs.writeFileSync(baselinePath, `${JSON.stringify(currentBaseline, null, 2)}\n`)
  console.log(`已写入前端构建基线：${path.relative(process.cwd(), baselinePath)}`)
  console.log(`- 首屏 JavaScript gzip：${formatBytes(current.metrics.initialJavaScriptGzipBytes)}`)
  console.log(`- 最大异步图表块：${formatBytes(current.metrics.maxAsyncChartRawBytes)}`)
  process.exit(0)
}

if (!fs.existsSync(baselinePath)) {
  console.error(`构建基线失败：未找到 ${baselinePath}`)
  console.error('请先执行 npm run build:baseline 生成经过 review 的基线。')
  process.exit(1)
}

let baseline
try {
  baseline = JSON.parse(fs.readFileSync(baselinePath, 'utf8'))
} catch (error) {
  console.error(`构建基线失败：无法读取 ${baselinePath}：${error instanceof Error ? error.message : String(error)}`)
  process.exit(1)
}

if (baseline.schemaVersion !== 1 || !baseline.metrics) {
  console.error(`构建基线失败：${baselinePath} 不是受支持的 schemaVersion=1 文件`)
  process.exit(1)
}

const comparisons = [
  { key: 'entryGzipBytes', label: '入口 JavaScript gzip', maxIncrease: 0.08 },
  { key: 'initialJavaScriptGzipBytes', label: '首屏 JavaScript gzip', maxIncrease: 0.08 },
  { key: 'maxInitialChunkRawBytes', label: '最大首屏块 raw', maxIncrease: 0.08 },
  { key: 'maxAsyncChartRawBytes', label: '最大异步图表块 raw', maxIncrease: 0.12 },
  { key: 'maxAsyncChartGzipBytes', label: '最大异步图表块 gzip', maxIncrease: 0.12 },
]
const failures = []

console.log(`构建历史基线对比：${baseline.baselineName || path.basename(baselinePath)}`)
for (const comparison of comparisons) {
  const currentValue = Number(current.metrics[comparison.key] || 0)
  const baselineValue = Number(baseline.metrics[comparison.key] || 0)
  const change = baselineValue === 0 ? 0 : (currentValue - baselineValue) / baselineValue
  console.log(`- ${comparison.label}：当前 ${formatBytes(currentValue)}，基线 ${formatBytes(baselineValue)}，变化 ${formatChange(currentValue, baselineValue)}`)
  if (change > comparison.maxIncrease) {
    failures.push(`${comparison.label}相对基线增加超过 ${(comparison.maxIncrease * 100).toFixed(0)}%`)
  }
}

const baselineInitialChartAssets = Array.isArray(baseline.initialChartAssets)
  ? baseline.initialChartAssets
  : []
if (current.initialChartAssets.length > 0) {
  failures.push(`图表依赖进入首屏：${current.initialChartAssets.map((item) => item.name).join(', ')}`)
}
if (baselineInitialChartAssets.length === 0 && current.initialChartAssets.length > 0) {
  failures.push('首屏相对基线新增图表依赖')
}

const currentAsyncCount = current.metrics.asyncChartChunkCount
const baselineAsyncCount = Number(baseline.metrics.asyncChartChunkCount || 0)
console.log(`- 异步图表块数量：当前 ${currentAsyncCount}，基线 ${baselineAsyncCount}，变化 ${formatChange(currentAsyncCount, baselineAsyncCount)}`)
if (baselineAsyncCount > 0 && currentAsyncCount > Math.ceil(baselineAsyncCount * 1.2)) {
  failures.push('异步图表块数量相对基线增加超过 20%')
}

if (failures.length > 0) {
  console.error('构建历史基线未通过：')
  for (const failure of failures) console.error(`- ${failure}`)
  process.exit(1)
}

console.log('构建历史基线通过。')
