import { reactive } from 'vue'
export const notice = reactive({ text: '', kind: 'success' })
let timer: ReturnType<typeof setTimeout>
export function notify(text: string, kind = 'success') { notice.text = text; notice.kind = kind; clearTimeout(timer); timer = setTimeout(() => { notice.text = '' }, 5500) }
export const directions: Record<string, string> = { backend: '后端开发', frontend: '前端开发', mobile: '移动端 / 客户端', embedded: '嵌入式', test: '测试', operations_sre: '运维 / SRE', security: '网络安全', data: '数据', algorithm_ai: '算法 / AI', unknown: '方向待确认' }
export const statuses: Record<string, string> = { draft: '待投递', submitted: '已投递', written_test: '笔试中', interview: '面试中', offer: '已获 Offer', rejected: '未通过', withdrawn: '已撤回' }
export const publicStatuses: Record<string, string> = { open: '公开招聘中', closed: '已截止 / 关闭', unknown: '状态待确认' }
export const recruitmentTypes: Record<string, string> = { campus: '校园招聘', internship: '实习', early_career: '应届 / 低经验社招' }
export const writtenTests: Record<string, string> = { yes: '有笔试', no: '官方明确无笔试', unknown: '笔试未公布' }
export function date(value?: string | null) { if (!value) return '未公布'; return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value)) }
export function time(value?: string | null) { if (!value) return '尚未核验'; return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value)) }
export function errorText(error: unknown) { return error instanceof Error ? error.message : '操作未完成，请稍后重试。' }
export function processingError(code: string) {
  const messages: Record<string,string> = {invalid_document:'文件无法可靠提取，请换用文字版 PDF 或 DOCX。',extractor_unavailable:'本地提取服务暂不可用。',model_not_configured:'模型尚未配置。',invalid_model_output:'模型输出未通过格式或原文依据校验，请重试或手动确认。',model_rejected:'模型服务拒绝请求，请联系管理员检查配置。',model_unavailable:'模型服务暂时不可用，请稍后重试。',cancelled:'权限、资料或岗位已变化，任务已取消。',interrupted:'任务已中断，可重新提交。'}
  return messages[code] ?? '处理未完成，请稍后重试。'
}
