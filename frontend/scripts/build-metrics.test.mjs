import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { collectBuildMetrics, collectStaticImports } from './build-metrics.mjs'

const temporaryDirectories = []

afterEach(() => {
  while (temporaryDirectories.length > 0) {
    fs.rmSync(temporaryDirectories.pop(), { recursive: true, force: true })
  }
})

describe('collectStaticImports', () => {
  it('collects static imports and re-exports without semicolons', () => {
    const source = `
      import {
        feature
      } from './feature.js'
      import './side-effect.js'
      import{compact}from './compact.js'
      export { exported } from './re-export.js'
      export*from './all.js'
      const deferred = import('./deferred.js')
    `

    expect(new Set(collectStaticImports(source))).toEqual(new Set([
      './feature.js',
      './side-effect.js',
      './compact.js',
      './re-export.js',
      './all.js',
    ]))
  })
})

describe('collectBuildMetrics', () => {
  it('follows JavaScript references with query strings and hashes', () => {
    const projectRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'ai-localbase-build-metrics-'))
    temporaryDirectories.push(projectRoot)
    const distRoot = path.join(projectRoot, 'dist')
    const assetsRoot = path.join(distRoot, 'assets')
    fs.mkdirSync(assetsRoot, { recursive: true })
    fs.writeFileSync(
      path.join(distRoot, 'index.html'),
      '<script type="module" src="/assets/entry.js?hash=entry"></script>',
    )
    fs.writeFileSync(
      path.join(assetsRoot, 'entry.js'),
      'import "./feature.js?hash=feature"\nexport * from "./re-export.js#module"\nconst deferred = import("./deferred.js")',
    )
    fs.writeFileSync(path.join(assetsRoot, 'feature.js'), 'export const feature = true')
    fs.writeFileSync(path.join(assetsRoot, 're-export.js'), 'export const exported = true')
    fs.writeFileSync(path.join(assetsRoot, 'deferred.js'), 'export const deferred = true')

    const build = collectBuildMetrics({ projectRoot })
    expect(build.initialJavaScript.map((item) => item.name).sort()).toEqual([
      'entry.js',
      'feature.js',
      're-export.js',
    ])
    expect(build.asyncStats.map((item) => item.name)).toEqual(['deferred.js'])
  })
})
