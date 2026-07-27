import fs from 'node:fs'

// backend/api/openapi.yaml 是 YAML，不是 JSON（spec.go 用 //go:embed openapi.yaml，
// 前端 openapi-typescript 也直接读它）。这里原本用 JSON.parse 解析，CI 里把它
// 重定向成 *.json 只是改了扩展名而已，于是每个 PR 的这一步都会抛 SyntaxError。
//
// 为避免给一个只在 CI 跑的校验脚本引入依赖，这里内置一个够用的 YAML 子集解析器：
// OpenAPI 契约只用到映射、序列、标量与块标量，不含锚点/别名/复杂键。
function parseYaml(text) {
  // 先把 "- key: value" 这种「列表项内联映射」展开成
  //   - (列表项标记)
  //     key: value
  // 之后解析器只需处理「纯列表项」和「纯映射项」两种行。
  const lines = []
  for (const raw of text.split(/\r?\n/)) {
    if (/^\s*#/.test(raw) || raw.trim() === '') continue
    const indent = raw.match(/^\s*/)[0].length
    let body = raw.trim()
    while (body.startsWith('- ')) {
      lines.push({ indent, item: true, text: '' })
      body = body.slice(2).trim()
    }
    if (body === '-') { lines.push({ indent, item: true, text: '' }); continue }
    if (body === '') continue
    const last = lines[lines.length - 1]
    if (last && last.item && last.indent === indent && last.text === '') lines.push({ indent: indent + 2, item: false, text: body })
    else lines.push({ indent, item: false, text: body })
  }

  let index = 0

  function stripComment(token) {
    // 剥掉未被引号包裹的行尾注释。不剥的话 `required: false   # optional` 会被解析成
    // 非空字符串（truthy），于是一个新增的可选参数会被误判成「新增必填参数」而拒绝 PR。
    let quote = null
    for (let i = 0; i < token.length; i++) {
      const char = token[i]
      if (quote) {
        if (char === quote) quote = null
      } else if (char === '"' || char === "'") {
        quote = char
      } else if (char === '#' && (i === 0 || /\s/.test(token[i - 1]))) {
        return token.slice(0, i).trim()
      }
    }
    return token
  }

  function parseScalar(raw) {
    const token = stripComment(raw)
    if (token === '' || token === '~' || token === 'null') return null
    if (token === 'true') return true
    if (token === 'false') return false
    if (/^-?\d+$/.test(token)) return Number(token)
    if (/^-?\d*\.\d+$/.test(token)) return Number(token)
    const quoted = token.match(/^'([\s\S]*)'$/) || token.match(/^"([\s\S]*)"$/)
    if (quoted) return quoted[1].replace(/\\"/g, '"').replace(/''/g, "'")
    return token
  }

  function skipBlock(indent) {
    // 块标量（| 与 >）的具体文本对「破坏性变更」判断没有意义，整体跳过。
    while (index < lines.length && lines[index].indent > indent) index++
    return ''
  }

  function parseNode(minIndent) {
    if (index >= lines.length) return null
    // 紧凑序列写法在 YAML 中合法且常见：
    //   parameters:
    //   - name: page          <- 序列项与 key 同缩进
    // 此时子节点的缩进比 minIndent 小 1。若在这里直接 return null 且不推进 index，
    // 外层 map 循环会立刻终止，从该点起的整份文档（包括后续所有 path）被静默丢弃——
    // 破坏性变更检查随之失效，而缩进风格一变又会误报 removed path。
    if (lines[index].indent < minIndent && !(lines[index].item && lines[index].indent === minIndent - 1)) return null
    const indent = lines[index].indent
    if (lines[index].item) {
      const list = []
      while (index < lines.length && lines[index].indent === indent && lines[index].item) {
        index++
        list.push(parseNode(indent + 1))
      }
      return list
    }
    const map = {}
    while (index < lines.length && lines[index].indent === indent && !lines[index].item) {
      const line = lines[index].text
      const separator = line.indexOf(':')
      if (separator < 0) { index++; continue }
      const key = parseScalar(line.slice(0, separator).trim())
      const value = line.slice(separator + 1).trim()
      index++
      if (value === '|' || value === '>' || value === '|-' || value === '>-' || value === '|+' || value === '>+') map[key] = skipBlock(indent)
      else if (value === '') map[key] = parseNode(indent + 1)
      else map[key] = parseScalar(value)
    }
    return map
  }

  return parseNode(lines.length ? lines[0].indent : 0) || {}
}

function load(path) {
  const text = fs.readFileSync(path, 'utf8')
  if (/^\s*\{/.test(text)) return JSON.parse(text)
  return parseYaml(text)
}

const [baselinePath, currentPath] = process.argv.slice(2)
if (!baselinePath || !currentPath) throw new Error('usage: node check-openapi-breaking.mjs <baseline> <current>')
const baseline = load(baselinePath)
const current = load(currentPath)
const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'head', 'options'])
const failures = []

// 参数既可以声明在 pathItem 上、也可以声明在 operation 上，还可能是 $ref。
// 原实现只看 operation.parameters，且对 $ref 形式会产出 "undefined:undefined"
// 再被 required 过滤掉，等于静默跳过——这正是最容易漏掉破坏性变更的地方。
function resolve(document, node) {
  if (node && typeof node.$ref === 'string' && node.$ref.startsWith('#/')) {
    let target = document
    for (const segment of node.$ref.slice(2).split('/')) {
      target = target?.[segment.replace(/~1/g, '/').replace(/~0/g, '~')]
    }
    return target || node
  }
  return node
}

function parametersOf(document, pathItem, operation) {
  const combined = [...(pathItem?.parameters || []), ...(operation?.parameters || [])]
  return combined.map((item) => resolve(document, item)).filter((item) => item && item.name && item.in)
}

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
    const before = parametersOf(baseline, pathItem, operation)
    const after = parametersOf(current, current.paths[path], candidate)
    const afterKeys = new Set(after.map((item) => `${item.in}:${item.name}`))
    for (const item of before) {
      if (item.required && !afterKeys.has(`${item.in}:${item.name}`)) {
        failures.push(`removed required parameter ${item.in}:${item.name} from ${method.toUpperCase()} ${path}`)
      }
    }
    // 反向检查：新增必填参数、或把既有可选参数改成必填，同样会打断现有客户端。
    const beforeByKey = new Map(before.map((item) => [`${item.in}:${item.name}`, item]))
    for (const item of after) {
      if (!item.required) continue
      const key = `${item.in}:${item.name}`
      const previous = beforeByKey.get(key)
      if (!previous) failures.push(`added required parameter ${key} to ${method.toUpperCase()} ${path}`)
      else if (!previous.required) failures.push(`parameter ${key} became required on ${method.toUpperCase()} ${path}`)
    }
  }
}

if (failures.length) {
  console.error(failures.join('\n'))
  process.exit(1)
}
