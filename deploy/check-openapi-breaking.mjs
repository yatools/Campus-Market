import fs from 'node:fs'

const [baselinePath, currentPath] = process.argv.slice(2)
if (!baselinePath || !currentPath) throw new Error('usage: node check-openapi-breaking.mjs <baseline> <current>')
const baseline = JSON.parse(fs.readFileSync(baselinePath, 'utf8'))
const current = JSON.parse(fs.readFileSync(currentPath, 'utf8'))
const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])
const failures = []

for (const [path, pathItem] of Object.entries(baseline.paths || {})) {
  if (!current.paths?.[path]) {
    failures.push(`removed path ${path}`)
    continue
  }
  for (const [method, operation] of Object.entries(pathItem)) {
    if (!methods.has(method)) continue
    const candidate = current.paths[path]?.[method]
    if (!candidate) {
      failures.push(`removed operation ${method.toUpperCase()} ${path}`)
      continue
    }
    for (const status of Object.keys(operation.responses || {})) {
      if (!candidate.responses?.[status]) failures.push(`removed response ${status} from ${method.toUpperCase()} ${path}`)
    }
    const required = (operation.parameters || []).filter((item) => item.required).map((item) => `${item.in}:${item.name}`)
    const nextParameters = new Set((candidate.parameters || []).map((item) => `${item.in}:${item.name}`))
    for (const parameter of required) if (!nextParameters.has(parameter)) failures.push(`removed required parameter ${parameter} from ${method.toUpperCase()} ${path}`)
  }
}

if (failures.length) {
  console.error(failures.join('\n'))
  process.exit(1)
}
