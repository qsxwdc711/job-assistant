<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { api } from '../../shared/api/client'
import type { Choices, User } from '../../shared/api/types'
import { errorText, notify, time } from '../../shared/display'
import { useSession } from '../../stores/session'

interface Subscription { filters_query: string; mode: string; enabled: boolean; revision: number }
interface Channel { configured: boolean; enabled: boolean; has_secret: boolean; revision: number; key_configured: boolean; send_enabled: boolean }
interface Item { job_id: string; company: string; title: string; locations: string[]; needs_confirmation: boolean }
interface Batch { id: string; kind: string; state: string; error_code: string; attempts: number; cutoff: string; items: Item[]; sources: Record<string, string> }
interface Source { code: string; name: string; enabled: boolean }
interface Collection { id: string; source_code: string; state: string; fetched: number; published: number; attempts: number }
const session = useSession(), busy = ref(''), error = ref(''), batches = ref<Batch[]>([]), sources = ref<Source[]>([]), collections = ref<Collection[]>([])
const subscription = reactive<Subscription>({ filters_query: '', mode: 'filtered', enabled: false, revision: 0 })
const channel = reactive<Channel>({ configured: false, enabled: false, has_secret: false, revision: 0, key_configured: false, send_enabled: false })
const credentials = reactive({ webhook: '', secret: '' }), choices = ref<Choices>({ dictionary: [], companies: [], cities: [] }), source = ref('')
const filters = reactive<Record<string, string>>({ q: '', company: '', direction: '', major: '', city: '', industry: '', nature: '', tier: '', graduation_year: '', recruitment_type: '', written_test: '', deadline_before: '' })
const includeUnknown = ref(true), personalAllowed = computed(() => !!session.capabilities['recommendations.read'])
const loading = ref(true)
const historyOwner = ref(''), users = ref<User[]>([])
const options = (kind: string) => choices.value.dictionary.filter(item => item.kind === kind)
const states: Record<string, string> = { queued: '等待处理', sending: '发送中', sent: '已发送', failed: '失败', delivery_unknown: '送达未知，需管理员确认', cancelled: '已取消', simulated: '模拟完成，未发送到群', station_only: '仅站内通知', empty: '本次没有新机会', succeeded: '已完成', running: '进行中', not_completed: '未完成', restricted: '访问受限' }
const sourceNames: Record<string, string> = { 'tencent-campus': '腾讯校招', 'tencent-internships': '腾讯实习', 'huawei-campus': '华为校招', 'huawei-internships': '华为实习', 'cmb-campus': '招商银行' }
async function history() {
  const owner = historyOwner.value
  const endpoint = session.isAdmin && owner ? '/admin/notifications?owner_user_id=' + encodeURIComponent(owner) : '/me/notifications'
  const result = await api<{ items: Batch[] }>(endpoint)
  if (owner === historyOwner.value) batches.value = result.items
  if (session.isAdmin) collections.value = (await api<{ items: Collection[] }>('/admin/automation')).items
}
function changeHistoryOwner() { batches.value = []; return run('history', history) }
async function load() {
  loading.value = true
  error.value = ''
  try {
    const [sub, target, dictionaries] = await Promise.all([api<{ subscription: Subscription }>('/me/subscriptions'), api<{ channel: Channel }>('/me/notification-channel'), api<Choices>('/jobs/filters')])
    Object.assign(subscription, sub.subscription); Object.assign(channel, target.channel); choices.value = dictionaries
    const params = new URLSearchParams(subscription.filters_query)
    for (const key of Object.keys(filters)) filters[key] = params.getAll(key).join(',')
    includeUnknown.value = params.get('include_unknown') !== 'false'
    if (session.isAdmin) {
      const [sourceList, accountList] = await Promise.all([api<{ items: Source[] }>('/admin/sources'), api<{ items: User[] }>('/admin/users?limit=100')])
      sources.value = sourceList.items; users.value = accountList.items; source.value ||= sources.value.find(item => item.enabled)?.code ?? ''
    }
    await history()
  } catch (value) { error.value = errorText(value) } finally { loading.value = false }
}
async function run(key: string, action: () => Promise<void>) {
  if (busy.value) return
  busy.value = key
  try { await action() } catch (value) { notify(errorText(value), 'error') } finally { busy.value = '' }
}
function saveSubscription() { return run('subscription', async () => {
  const query = new URLSearchParams({ include_unknown: String(includeUnknown.value) })
  for (const [key, value] of Object.entries(filters)) if (value) query.set(key, value)
  const result = await api<{ subscription: Subscription }>('/me/subscriptions', { method: 'PUT', body: { ...subscription, filters_query: query.toString() } })
  Object.assign(subscription, result.subscription); notify('订阅已保存，从下一次摘要开始采用这些条件。')
}) }
function saveChannel() { return run('channel', async () => {
  const result = await api<{ channel: Channel }>('/me/notification-channel', { method: 'PUT', body: { enabled: channel.enabled, revision: channel.revision, ...(credentials.webhook.trim() ? { webhook: credentials.webhook.trim() } : {}), ...(credentials.secret.trim() ? { secret: credentials.secret.trim() } : {}) } })
  Object.assign(channel, result.channel); credentials.webhook = ''; credentials.secret = ''; notify('飞书接收配置已保存。')
}) }
function clearChannel() { return run('channel', async () => { await api('/me/notification-channel', { method: 'DELETE', body: { revision: channel.revision } }); credentials.webhook = ''; credentials.secret = ''; await load(); notify('飞书接收配置已清除。') }) }
function testChannel() { return run('test', async () => { await api('/me/notification-channel/test', { method: 'POST' }); notify(channel.send_enabled ? '连接测试已排队，请查看发送记录。' : '模拟连接测试已排队，不会发送到真实群。'); await history() }) }
function requestDigest() { return run('digest', async () => { await api('/admin/automation/digest', { method: 'POST' }); notify('已排队为启用订阅的账号生成摘要。'); await history() }) }
function requestCollection() { return run('collect', async () => { await api('/admin/automation/collect', { method: 'POST', body: { source_code: source.value } }); notify('岗位更新已排队。'); await history() }) }
function resolve(batch: Batch, decision: string) { return run(batch.id, async () => { await api('/admin/notifications/' + batch.id + '/resolve', { method: 'POST', body: { decision } }); await history(); notify('已记录确认结果。') }) }
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { void load(); timer = setInterval(() => { void history().catch(() => {}) }, 10000) })
onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <section class="page-heading"><div><span class="eyebrow">把合适的机会，留在视线里</span><h1>订阅与通知</h1><p>每天北京时间 08:00 更新岗位，09:07 生成摘要。没有新事件时，不发送重复提醒。</p></div><button class="button secondary" :disabled="!!busy" @click="load">刷新配置</button></section>
  <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
  <p v-if="loading" class="loading-state" role="status">正在读取订阅设置…</p>
  <div v-else class="notification-grid">
    <section class="panel notification-panel"><h2>我的岗位订阅</h2><p class="muted">筛选只是发现机会；具体能否投递，仍需核对官方条件。首次启用会纳入现有匹配岗位。</p>
      <form @submit.prevent="saveSubscription"><div class="notification-fields">
        <label>岗位关键词<input v-model="filters.q" maxlength="120" placeholder="例如：Go、后端、实习" /></label><label>公司名称<input v-model="filters.company" maxlength="80" /></label>
        <label v-for="field in [{ key: 'direction', label: '岗位方向', kind: 'direction' }, { key: 'major', label: '专业', kind: 'major' }, { key: 'industry', label: '行业', kind: 'industry' }, { key: 'nature', label: '企业性质', kind: 'nature' }, { key: 'tier', label: '企业规模', kind: 'tier' }]" :key="field.key">{{ field.label }}<select v-model="filters[field.key]"><option value="">全部</option><option v-for="item in options(field.kind)" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>工作城市<select v-model="filters.city"><option value="">全部</option><option value="overseas">海外</option><option v-for="city in choices.cities" :key="city.code" :value="city.code">{{ city.city }}</option></select></label>
        <label>招聘类型<select v-model="filters.recruitment_type"><option value="">全部机会</option><option value="campus">校招全职</option><option value="internship">实习</option><option value="early_career">应届／低经验社招</option></select></label>
        <label>毕业年份<input v-model="filters.graduation_year" type="number" min="2000" max="2100" placeholder="不限" /></label>
        <label>笔试<select v-model="filters.written_test"><option value="">全部</option><option value="yes">有笔试</option><option value="no">无笔试</option><option value="unknown">待确认</option></select></label><label>截止时间不晚于<input v-model="filters.deadline_before" type="date" /></label>
      </div><label class="notification-check"><input v-model="includeUnknown" type="checkbox" />保留官方未披露条件的机会</label>
      <label>摘要类型<select v-model="subscription.mode"><option value="filtered">按筛选条件订阅</option><option value="personalized" :disabled="!personalAllowed">按当前简历的有效匹配结果订阅（VIP）</option></select></label>
      <p v-if="subscription.mode === 'personalized'" class="field-hint">先在岗位匹配中生成结果。简历、岗位或权限变化后，旧结果不会继续用于推送。</p>
      <label class="notification-check"><input v-model="subscription.enabled" type="checkbox" />启用我的订阅</label><button class="button primary" :disabled="!!busy">保存订阅</button></form>
    </section>
    <section class="panel notification-panel"><h2>飞书接收群</h2><p class="muted">每个账号独立绑定一个机器人。群内只展示公开岗位信息，不发送简历内容、技能差距或投递备注。</p>
      <p v-if="!channel.send_enabled" class="notification-info">当前为模拟验收模式：仅保留站内结果，不向真实群发送消息。</p>
      <p v-if="!channel.key_configured" class="inline-error">服务尚未配置接收信息的加密密钥，请联系管理员。</p>
      <form @submit.prevent="saveChannel"><label>飞书机器人 Webhook<input v-model="credentials.webhook" type="password" autocomplete="new-password" :placeholder="channel.configured ? '已保存；留空保留原地址' : 'https://open.feishu.cn/open-apis/bot/v2/hook/…'" /></label><label>机器人签名密钥（可选）<input v-model="credentials.secret" type="password" autocomplete="new-password" :placeholder="channel.has_secret ? '已保存；留空保留原密钥' : '机器人开启签名校验时填写'" /></label>
      <p class="field-hint">保存后不回显地址与密钥。机器人若使用关键词校验，请包含“求职助手”。</p><label class="notification-check"><input v-model="channel.enabled" type="checkbox" />启用飞书接收</label>
      <div class="notification-actions"><button class="button primary" :disabled="!!busy || !channel.key_configured">保存接收配置</button><button class="button secondary" type="button" :disabled="!!busy || !channel.configured || !channel.enabled" @click="testChannel">{{ channel.send_enabled ? '发送连接测试' : '模拟连接测试' }}</button><button class="button ghost" type="button" :disabled="!!busy || !channel.configured" @click="clearChannel">清除接收配置</button></div></form>
    </section>
  </div>
  <section v-if="session.isAdmin" class="panel notification-panel"><h2>管理员更新操作</h2><p class="muted">可单独更新一个来源，或为所有已启用订阅的账号生成本次摘要。晚于每日截止时间的新结果会进入下一次摘要。</p><div class="notification-actions"><select v-model="source" aria-label="要更新的来源"><option v-for="entry in sources.filter(item => item.enabled)" :key="entry.code" :value="entry.code">{{ entry.name }}</option></select><button class="button secondary" :disabled="!!busy || !source" @click="requestCollection">更新选中来源</button><button class="button secondary" :disabled="!!busy" @click="requestDigest">立即生成摘要</button></div><p v-for="entry in collections.slice(0,5)" :key="entry.id" class="field-hint">{{ sourceNames[entry.source_code] ?? '招聘来源' }} · {{ states[entry.state] ?? entry.state }} · 发布 {{ entry.published }} 个广告</p></section>
  <section class="panel notification-panel"><div class="notification-actions"><h2>站内通知与发送记录</h2><button class="button ghost" @click="history">刷新记录</button></div>
    <label v-if="session.isAdmin">查看账号的发送记录<select v-model="historyOwner" :disabled="!!busy" @change="changeHistoryOwner"><option value="">我自己</option><option v-for="user in users.filter(item => item.id !== session.user?.id)" :key="user.id" :value="user.id">{{ user.username }}{{ user.disabled ? '（已停用）' : '' }}</option></select><small>可选择前 100 个账号；此处只查看站内记录，不改变对方订阅或接收群。</small></label>
    <p v-if="!batches.length" class="muted">还没有摘要。保存并启用订阅后，可在这里查看新机会、来源更新情况和发送结果。</p>
    <article v-for="batch in batches" :key="batch.id" class="notification-batch"><div class="notification-actions"><strong>{{ batch.kind === 'test' ? '连接测试' : '岗位摘要' }}</strong><span class="tag" :class="{ warning: batch.state === 'delivery_unknown' || batch.state === 'failed' }">{{ states[batch.state] ?? batch.state }}</span><small class="muted">截至 {{ time(batch.cutoff) }}</small></div>
      <p v-if="batch.kind === 'digest'" class="field-hint">{{ batch.items.length }} 个岗位广告，已投递或失效的机会不会继续发送。</p><ul v-if="batch.items.length" class="notification-items"><li v-for="item in batch.items.slice(0,10)" :key="item.job_id"><RouterLink :to="'/jobs/' + item.job_id">{{ item.company }} · {{ item.title }}</RouterLink><small>{{ item.locations.join(' / ') }}{{ item.needs_confirmation ? ' · 条件待确认' : '' }}</small></li></ul><details v-if="batch.items.length > 10"><summary>查看其余 {{ batch.items.length - 10 }} 个机会</summary><ul class="notification-items"><li v-for="item in batch.items.slice(10)" :key="item.job_id"><RouterLink :to="'/jobs/' + item.job_id">{{ item.company }} · {{ item.title }}</RouterLink></li></ul></details>
      <details v-if="Object.keys(batch.sources).length"><summary>查看本次来源更新情况</summary><p v-for="(state,key) in batch.sources" :key="key" class="field-hint">{{ sourceNames[key] ?? '招聘来源' }}：{{ states[state] ?? '待核验' }}</p></details>
      <div v-if="batch.state === 'delivery_unknown' && session.isAdmin" class="notification-actions"><button class="button secondary" :disabled="!!busy" @click="resolve(batch,'received')">确认已收到</button><button class="button secondary" :disabled="!!busy" @click="resolve(batch,'not_received')">确认未收到，重试</button></div>
    </article>
  </section>
</template>

<style scoped>
.notification-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:24px}.notification-panel{padding:26px;min-width:0;margin-bottom:24px}.notification-panel h2{margin:0 0 14px}.notification-panel form{display:grid;gap:16px;margin-top:20px}.notification-fields{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.notification-check{display:flex;flex-direction:row;align-items:center;gap:8px}.notification-check input{width:16px;height:16px;min-height:16px;padding:0;flex:none}.notification-actions{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.notification-actions select{max-width:100%}.notification-info{padding:14px;border-radius:10px;background:#f7f1e8;color:#705331;font-size:13px}.notification-batch{padding:20px 0;border-top:1px solid var(--border,#e9e5dd)}.notification-items{padding-left:20px;display:grid;gap:12px}.notification-items li{overflow-wrap:anywhere}.notification-items small{display:block;color:#7b827c;margin-top:4px}.notification-batch summary{cursor:pointer;font-size:13px;margin:12px 0}@media(max-width:1000px){.notification-grid{grid-template-columns:1fr}}@media(max-width:600px){.notification-panel{padding:18px}.notification-fields{grid-template-columns:1fr}}
</style>
