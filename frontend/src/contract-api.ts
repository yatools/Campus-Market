import { api, json } from './api'
import type { paths } from './generated/api'

type HTTPMethod = 'get' | 'post' | 'put' | 'patch' | 'delete'
type Operation<Path extends keyof paths, Method extends HTTPMethod> = Method extends keyof paths[Path] ? NonNullable<paths[Path][Method]> : never
type RequestBody<Value> = Value extends { requestBody: { content: { 'application/json': infer Body } } } ? Body : never
type JSONResponse<Value, Status extends number> = Value extends { responses: infer Responses }
  ? Status extends keyof Responses
    ? Responses[Status] extends { content: { 'application/json': infer Body } } ? Body : undefined
    : never
  : never
type SuccessResponse<Value> = JSONResponse<Value, 200> | JSONResponse<Value, 201> | JSONResponse<Value, 202> | JSONResponse<Value, 204>

/** Typed client for literal OpenAPI paths. Dynamic path parameters remain in feature services. */
export function contractApi<Path extends keyof paths, Method extends HTTPMethod>(
  path: Path,
  method: Method,
  ...body: RequestBody<Operation<Path, Method>> extends never ? [] : [RequestBody<Operation<Path, Method>>]
): Promise<SuccessResponse<Operation<Path, Method>>> {
  const relativePath = String(path).replace(/^\/api\/v1/, '')
  const init = body.length ? json(method.toUpperCase(), body[0]) : { method: method.toUpperCase() }
  return api<SuccessResponse<Operation<Path, Method>>>(relativePath, init)
}
