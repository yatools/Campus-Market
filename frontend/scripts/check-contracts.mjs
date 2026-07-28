import fs from 'node:fs'

const spec = JSON.parse(fs.readFileSync(new URL('../../backend/api/openapi.yaml', import.meta.url), 'utf8'))
const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])
const operationIds = new Map()
const failures = []
let typedOperations = 0

function isBroadSchema(schema) {
  if (!schema || typeof schema !== 'object') return true
  if (schema.$ref) return false
  if (schema.additionalProperties === true && !schema.properties) return true
  if (schema.type === 'object' && !schema.properties && !schema.additionalProperties) return true
  return false
}

for (const [path, pathItem] of Object.entries(spec.paths ?? {})) {
  for (const [method, operation] of Object.entries(pathItem)) {
    if (!methods.has(method)) continue
    const operationId = operation.operationId
    if (!operationId) {
      failures.push(`${method.toUpperCase()} ${path} has no operationId`)
      continue
    }
    if (operationIds.has(operationId)) {
      failures.push(`duplicate operationId ${operationId}: ${operationIds.get(operationId)} and ${method.toUpperCase()} ${path}`)
    }
    operationIds.set(operationId, `${method.toUpperCase()} ${path}`)

    if (operation['x-contract-status'] !== 'typed') continue
    typedOperations += 1
    if (!/^[a-z][A-Za-z0-9]*$/.test(operationId)) {
      failures.push(`${operationId} is not a stable lower-camel-case operationId`)
    }

    const successResponses = Object.entries(operation.responses ?? {}).filter(([status]) => /^2\d\d$/.test(status))
    if (!successResponses.length) failures.push(`${operationId} has no success response`)
    for (const [, response] of successResponses) {
      const jsonSchema = response.content?.['application/json']?.schema
      if (response.content?.['application/json'] && isBroadSchema(jsonSchema)) {
        failures.push(`${operationId} has a broad JSON success response`)
      }
    }

    const requestSchema = operation.requestBody?.content?.['application/json']?.schema
    if (operation.requestBody?.content?.['application/json'] && isBroadSchema(requestSchema)) {
      failures.push(`${operationId} has a broad JSON request body`)
    }

    for (const status of ['400', '401', '403', '404', '409', '422', '500']) {
      const schema = operation.responses?.[status]?.content?.['application/json']?.schema
      if (schema?.$ref !== '#/components/schemas/ErrorResponse') {
        failures.push(`${operationId} must declare ${status} as ErrorResponse`)
      }
    }
  }
}

if (typedOperations < 120) {
  failures.push(`typed contract coverage regressed: expected at least 120 operations, found ${typedOperations}`)
}

if (failures.length) {
  console.error(failures.join('\n'))
  process.exit(1)
}

console.log(`${typedOperations} operations have strict migrated contracts; ${operationIds.size} operationIds are unique`)
