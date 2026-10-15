<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, ApiError } from '../../shared/api/client'
import type { Application, ApplicationEvent, Page } from '../../shared/api/types'
import { statuses, publicStatuses, date, time, notify, errorText } from '../../shared/display'
import Icon from '../../shared/Icon.vue'
const route = useRoute(), router = useRouter()
const page = ref<Page<Application>>({ items: [], total: 0, limit: 20, offset: 0 }), loading = ref(false), error = ref(''), selected = ref<Application | null>(null), events = ref<ApplicationEvent[]>([])
const dialog = ref<HTMLDialogElement | null>(null), saving = ref(false), editError = ref(''), conflict = ref(false), tab = ref('all')
const form = reactive({ status: 'draft', notes: '', next_action: '', next_action_at: '' })
const visible = computed(() => page.value.items.filter(item => tab.value === 'all' || item.status === tab.value))
function localTime(value: string | null) { if (!value) return ''; const d = new Date(value), two = (n: number) => String(n).padStart(2, '0'); return `${d.getFullYear()}-${two(d.getMonth()+1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}` }
async function load(offset = Number(route.query.offset ?? 0)) { loading.value = true; error.value = ''; try { page.value = await api<Page<Application>>('/me/applications?limit=20&offset=' + offset) } catch (value) { error.value = errorText(value) } finally { loading.value = false } }
async function history(id: string) { const result = await api<{items: ApplicationEvent[]}>('/me/applications/' + id + '/events'); events.value = result.items }
async function open(record: Application) { selected.value = record; Object.assign(form, { status: record.status, notes: record.notes, next_action: record.next_action, next_action_at: localTime(record.next_action_at) }); editError.value = ''; conflict.value = false; events.value = []; dialog.value?.showModal(); try { await history(record.id) } catch (value) { editError.value = errorText(value) } }
async function refreshRecord() {
  if (!selected.value) return
  try { const result = await api<{application: Application}>('/me/applications/' + selected.value.id); selected.value = result.application; conflict.value = false; editError.value = '已读取最新版本。你尚未保存的输入已保留，请核对后再保存。'; await history(result.application.id) }
  catch (value) { editError.value = errorText(value) }
}
async function save() {
  if (!selected.value) return; saving.value = true; editError.value = ''
  try {
    const result = await api<{application: Application}>('/me/applications/' + selected.value.id, { method: 'PATCH', body: { status: form.status, notes: form.notes, next_action: form.next_action, next_action_at: form.next_action_at ? new Date(form.next_action_at).toISOString() : null, version: selected.value.version } })
    selected.value = result.application; conflict.value = false; await load(page.value.offset); await history(result.application.id); notify('进度已保存，其他设备也能看到。')
  } catch (value) { editError.value = errorText(value); conflict.value = value instanceof ApiError && value.code === 'version_conflict' } finally { saving.value = false }
}
async function remove() {
  if (!selected.value || !window.confirm('删除这条投递记录及其历史？此操作不会撤销官网申请。')) return
  saving.value = true
  try { await api('/me/applications/' + selected.value.id, { method: 'DELETE', body: { version: selected.value.version } }); dialog.value?.close(); await load(); notify('已删除本地投递记录') }
  catch (value) { editError.value = errorText(value); conflict.value = value instanceof ApiError && value.code === 'version_conflict' } finally { saving.value = false }
}
async function paginate(delta: number) { const offset = Math.max(0, page.value.offset + delta * 20); await router.replace({ query: { offset } }); await load(offset) }
void load()
</script>
<template>
  <section class="page-heading"><div><span class="eyebrow">每一次推进，都值得记下</span><h1>我的投递进度</h1><p>记录申请、准备事项与反馈。状态可以按实际情况修正。</p></div><RouterLink class="button primary" to="/jobs"><Icon name="plus" />发现新机会</RouterLink></section>
  <div class="progress-summary panel"><div><span>全部投递记录</span><strong>{{ page.total }}</strong><small>仅当前账号</small></div><div><Icon name="shield" /><p>官方岗位关闭后，<br>你的进度和历史仍会保留。</p></div><div><Icon name="clock" /><p>多设备共同管理，<br>版本冲突会提示你确认。</p></div></div>
  <section class="panel applications-panel"><div class="type-tabs progress-tabs"><button :class="{ active: tab === 'all' }" @click="tab = 'all'">本页全部</button><button v-for="(label, key) in statuses" :key="key" :class="{ active: tab === key }" @click="tab = key">{{ label }}</button></div><div v-if="error" class="empty-state" role="alert"><h2>暂时无法加载</h2><p>{{ error }}</p><button class="button secondary" @click="load()">重新加载</button></div><div v-else-if="loading" class="loading-state"><span class="spinner"></span>正在读取进度…</div><div v-else-if="!visible.length" class="empty-state"><Icon name="briefcase" /><h2>{{ page.total ? '本页没有这个状态的记录' : '开始记录你的第一份机会' }}</h2><p>在岗位详情点击“记录投递”，再回来补充状态与准备事项。</p><RouterLink class="button secondary" to="/jobs">前往机会看板</RouterLink></div><div v-else class="application-rows"><div class="application-table-head"><span>公司 / 岗位</span><span>我的进度</span><span>下一步事项</span><span>最近修改</span><span></span></div><article v-for="record in visible" :key="record.id" class="application-row"><div><small>{{ record.job.company_name }}</small><h2>{{ record.job.title }}</h2><span v-if="record.job.public_status === 'closed' || !record.job.published" class="closed-note">{{ !record.job.published ? '公共岗位已隐藏，个人记录保留' : publicStatuses[record.job.public_status] }}</span></div><span class="application-status" :class="record.status">{{ statuses[record.status] }}</span><div class="next-action"><span>{{ record.next_action || '还没有安排下一步' }}</span><small v-if="record.next_action_at">{{ date(record.next_action_at) }}</small></div><small class="record-updated">{{ time(record.updated_at) }}</small><button class="button secondary compact" @click="open(record)">管理进度<Icon name="chevron" /></button></article></div><div class="pagination"><span>第 {{ Math.floor(page.offset / 20) + 1 }} 页 · 共 {{ page.total }} 条记录</span><div><button class="button ghost compact" :disabled="!page.offset || loading" @click="paginate(-1)">上一页</button><button class="button ghost compact" :disabled="page.offset + 20 >= page.total || loading" @click="paginate(1)">下一页</button></div></div></section>
  <dialog ref="dialog" class="edit-dialog" @click="($event.target === dialog) && dialog?.close()"><form v-if="selected" @submit.prevent="save"><header class="dialog-header"><div><span class="eyebrow">{{ selected.job.company_name }}</span><h2>{{ selected.job.title }}</h2></div><button type="button" class="icon-button" aria-label="关闭投递编辑" @click="dialog?.close()"><Icon name="close" /></button></header><div class="dialog-content"><div class="form-row"><label>当前进度<select v-model="form.status" aria-label="当前进度"><option v-for="(label, key) in statuses" :key="key" :value="key">{{ label }}</option></select></label><label>事项时间<input v-model="form.next_action_at" type="datetime-local" /></label></div><label>下一步事项<input v-model="form.next_action" maxlength="200" placeholder="例如：复习并发基础、准备技术面" /></label><label>个人备注<textarea v-model="form.notes" rows="4" maxlength="4000" placeholder="记录投递渠道、面试反馈或需要准备的内容" /></label><p v-if="editError" class="inline-error" role="alert">{{ editError }}</p><button v-if="conflict" type="button" class="button secondary" @click="refreshRecord">刷新最新记录，保留输入</button><div class="edit-hint">只更新个人进度，不会替你操作官网申请。</div><details class="history" open><summary>进度历史 <span>{{ events.length === 100 ? '最近 100 条' : events.length + ' 条' }}</span></summary><ol><li v-for="event in events" :key="event.id"><span class="history-dot"></span><strong>{{ statuses[event.status] }}</strong><small>{{ time(event.created_at) }} · 第 {{ event.version }} 版</small><p v-if="event.next_action">{{ event.next_action }}</p><p v-if="event.notes" class="muted">{{ event.notes }}</p></li></ol></details></div><footer class="dialog-footer"><button type="button" class="text-button danger" :disabled="saving" @click="remove">删除记录</button><div><button type="button" class="button ghost" @click="dialog?.close()">关闭</button><button class="button primary" :disabled="saving || conflict">{{ saving ? '保存中…' : '保存进度' }}</button></div></footer></form></dialog>
</template>
