import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../', import.meta.url))
const allowlist = JSON.parse(fs.readFileSync(new URL('./legacy-api-allowlist.json', import.meta.url), 'utf8'))
const failures = []

function visit(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    if (entry.name === 'generated') continue
    const target = path.join(directory, entry.name)
    if (entry.isDirectory()) {
      visit(target)
      continue
    }
    if (!/\.(ts|vue)$/.test(entry.name) || /\.test\.ts$/.test(entry.name)) continue
    const relative = path.relative(root, target).replaceAll('\\', '/')
    if (relative === 'src/api.ts' || relative === 'src/contract-api.ts') continue
    const source = fs.readFileSync(target, 'utf8')
    const calls = source.match(/\bapi(?:<[^\n(]*>)?\s*\(/g)?.length ?? 0
    const allowance = allowlist[relative] ?? 0
    if (calls > allowance) failures.push(`${relative} has ${calls} legacy api() calls; allowance is ${allowance}`)
  }
}

visit(fileURLToPath(new URL('../src', import.meta.url)))

if (failures.length) {
  console.error(failures.join('\n'))
  process.exit(1)
}

console.log('No new legacy api() calls were introduced')
