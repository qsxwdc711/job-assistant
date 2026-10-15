const messages: Record<string, string> = {
  unauthenticated: '登录状态已失效，请重新登录。', invalid_credentials: '账号或密码不正确。',
  forbidden: '当前账号没有这项操作权限。', not_found: '这条记录不存在，或你没有访问权限。',
  invalid_input: '填写的信息不符合要求，请检查后重试。', version_conflict: '这条记录已在其他设备修改。请刷新记录后再保存。',
  application_exists: '这个岗位已有投递记录，可在投递管理中查看。', user_exists: '这个账号名已被使用。',
  last_administrator: '需要至少保留一位启用的管理员。', rate_limited: '操作过于频繁，请稍后再试。',
  invalid_document: '文件无法提取。请上传不超过 10 MiB 的 PDF 或 DOCX；加密、损坏及扫描图片简历暂不支持。',
  body_too_large: '文件或内容超过大小限制。', model_not_configured: '模型尚未配置，可继续本地提取、手动确认和资格检查。',
  profile_changed: '资料已更新、尚未确认，或仍有任务处理中。请刷新后重试。',
}
export class ApiError extends Error {
  constructor(public status: number, public code: string, message?: string) { super(message ?? messages[code] ?? '操作未完成，请稍后重试。') }
}
let hooks = { token: () => '', unauthorized: () => {}, forbidden: () => {} }
export function configureApi(value: typeof hooks) { hooks = value }
export async function api<T>(path: string, options: {method?: string; body?: unknown; silentAuth?: boolean} = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers = new Headers({ Accept: 'application/json' })
  const multipart = options.body instanceof FormData
  if (options.body !== undefined && !multipart) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD'].includes(method) && hooks.token()) headers.set('X-CSRF-Token', hooks.token())
  const controller = new AbortController()
  const timeout = window.setTimeout(() => controller.abort(), 18000)
  try {
    const response = await fetch('/api/v1' + path, { method, headers, credentials: 'same-origin', body: options.body === undefined ? undefined : multipart ? options.body as FormData : JSON.stringify(options.body), signal: controller.signal })
    if (response.status === 204) return undefined as T
    const result = await response.json()
    if (!response.ok) {
      if (!options.silentAuth && response.status === 401) hooks.unauthorized()
      if (!options.silentAuth && response.status === 403) hooks.forbidden()
      throw new ApiError(response.status, result.error?.code ?? 'request_failed')
    }
    return result as T
  } catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError(0, 'network_error', '暂时无法连接，请检查网络后重试。')
  } finally { window.clearTimeout(timeout) }
}
