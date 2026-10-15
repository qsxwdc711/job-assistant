<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../../shared/api/client'
import type { Choices, Job, JobState, Page, Variant } from '../../shared/api/types'
import { date, time, directions, publicStatuses, recruitmentTypes, writtenTests, notify, errorText } from '../../shared/display'
import Icon from '../../shared/Icon.vue'
const route = useRoute(), router = useRouter()
const isFavorites = computed(() => route.name === 'favorites')
const fields = reactive<Record<string, string>>({ q: '', company: '', direction: '', major: '', city: '', industry: '', nature: '', tier: '', graduation_year: '', recruitment_type: '', written_test: '', deadline_before: '', status: '', include_unknown: 'true', sort: 'updated' })
const choices = ref<Choices>({ dictionary: [], cities: [], companies: [] }), page = ref<Page<Job>>({ items: [], total: 0, limit: 20, offset: 0 })
const loading = ref(false), error = ref(''), filterOpen = ref(false), favorites = ref(new Set<string>()), recorded = ref(new Set<string>()), pending = ref(new Set<string>())
const view = computed(() => route.query.view === 'table' ? 'table' : 'list')
const activeCount = computed(() => Object.entries(fields).filter(([key, value]) => value && !['include_unknown', 'sort', 'q'].includes(key)).length)
const options = (kind: string) => choices.value.dictionary.filter(item => item.kind === kind)
const cityOptions = computed(() => choices.value.cities.filter(item => item.kind === 'domestic'))
let requestID = 0
function query() { const values = new URLSearchParams(); for (const [key, value] of Object.entries(route.query)) { if (key === 'view') continue; for (const item of Array.isArray(value) ? value : [value]) if (item != null) values.append(key, item) }; return values.toString() }
async function load() {
  const ticket = ++requestID; loading.value = true; error.value = ''
  try {
    let next: Page<Job>
    if (isFavorites.value) {
      const offset = Number(route.query.offset ?? 0)
      const data = await api<{ items: {job: Job}[] }>('/me/favorites?limit=20&offset=' + offset)
      next = { items: data.items.map(item => item.job), total: offset + data.items.length, limit: 20, offset }
    } else { next = await api<Page<Job>>('/jobs?' + query()) }
    if (ticket !== requestID) return
    const ids = [...new Set(next.items.map(item => item.id))]
    const params = new URLSearchParams()
    for (const id of ids) params.append('job_id', id)
    const states = ids.length ? await api<{items: JobState[]}>('/me/job-states?' + params) : { items: [] }
    if (ticket !== requestID) return
    page.value = next
    favorites.value = new Set(states.items.filter(item => item.favorite).map(item => item.job_id))
    recorded.value = new Set(states.items.filter(item => item.recorded).map(item => item.job_id))
  } catch (value) { if (ticket === requestID) error.value = errorText(value) } finally { if (ticket === requestID) loading.value = false }
}
watch(() => route.fullPath, () => {
  for (const key of Object.keys(fields)) fields[key] = String(route.query[key] ?? (key === 'include_unknown' ? 'true' : key === 'sort' ? 'updated' : ''))
  void load()
}, { immediate: true })
void api<Choices>('/jobs/filters').then(value => { choices.value = value }).catch(value => { error.value = errorText(value) })
function apply() { const next: Record<string, string> = { view: view.value }; for (const [key, value] of Object.entries(fields)) if (value) next[key] = value; void router.push({ path: '/jobs', query: next }); filterOpen.value = false }
function reset() { void router.push({ path: '/jobs', query: { view: view.value } }) }
function changeView(value: string) { void router.replace({ query: { ...route.query, view: value } }) }
function paginate(delta: number) { void router.push({ query: { ...route.query, offset: String(Math.max(0, page.value.offset + delta * 20)) } }) }
function variants(job: Job): Variant[] {
  const matched = job.variants.filter(v => (!fields.direction || v.direction === fields.direction) && (!fields.city || (v.locations ?? []).some(p => p.code === fields.city || p.city === fields.city || p.kind === fields.city)))
  return matched.length ? matched : job.variants
}
function places(job: Job) { return [...new Set(variants(job).flatMap(v => (v.locations ?? []).map(p => p.city)))].join(' / ') || '地点未公布' }
async function favorite(job: Job) {
  if (pending.value.has(job.id)) return; pending.value.add(job.id)
  const saved = favorites.value.has(job.id)
  try { await api('/me/favorites/' + job.id, { method: saved ? 'DELETE' : 'PUT' }); if (saved) favorites.value.delete(job.id); else favorites.value.add(job.id); if (isFavorites.value && saved) await load(); notify(saved ? '已取消收藏' : '已加入收藏') }
  catch (value) { notify(errorText(value), 'error') } finally { pending.value.delete(job.id) }
}
async function record(job: Job) {
  if (recorded.value.has(job.id)) { await router.push('/applications'); return }
  if (pending.value.has(job.id)) return; pending.value.add(job.id)
  try { await api('/me/applications', { method: 'POST', body: { job_id: job.id, status: 'draft' } }); recorded.value.add(job.id); notify('已记录为待投递，请通过官方入口完成申请。') }
  catch (value) { notify(errorText(value), 'error') } finally { pending.value.delete(job.id) }
}
</script>
<template>
  <section class="page-heading"><div><span class="eyebrow">{{ isFavorites ? '为喜欢的机会留一个位置' : '官方岗位 · 计算机方向' }}</span><h1>{{ isFavorites ? '我的收藏' : '下一份机会，认真挑选。' }}</h1><p>{{ isFavorites ? '收藏不会代替投递，公开岗位变化也不会清空你的记录。' : '把筛选交给条件，把选择留给自己。每条机会都能追溯到官方。' }}</p></div><span class="heading-decoration"><Icon :name="isFavorites ? 'bookmark' : 'briefcase'" /></span></section>
  <div class="jobs-workspace" :class="{ 'favorites-workspace': isFavorites }">
    <div v-if="filterOpen" class="filter-scrim" @click="filterOpen = false"></div>
    <aside v-if="!isFavorites" class="filter-panel" :class="{ 'mobile-open': filterOpen }" aria-label="岗位筛选">
      <div class="filter-title"><strong><Icon name="filter" />筛选机会 <span v-if="activeCount" class="count-badge">{{ activeCount }}</span></strong><button class="text-button" @click="reset">重置</button><button class="icon-button filter-close" aria-label="关闭筛选" @click="filterOpen = false"><Icon name="close" /></button></div>
      <form class="filter-fields" @submit.prevent="apply">
        <label>岗位方向<select v-model="fields.direction"><option value="">全部方向</option><option v-for="item in options('direction')" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>专业要求<select v-model="fields.major"><option value="">全部专业要求</option><option v-for="item in options('major')" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>工作地点<select v-model="fields.city"><option value="">全国 / 海外</option><option value="overseas">海外</option><option v-for="item in cityOptions" :key="item.code" :value="item.code">{{ item.city }}</option></select></label>
        <label>公司<input v-model="fields.company" maxlength="80" placeholder="输入公司名称" /></label>
        <label>行业<select v-model="fields.industry"><option value="">全部行业</option><option v-for="item in options('industry')" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>企业性质<select v-model="fields.nature"><option value="">全部性质</option><option v-for="item in options('nature')" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>企业规模标签<select v-model="fields.tier"><option value="">全部规模</option><option v-for="item in options('tier')" :key="item.code" :value="item.code">{{ item.name }}</option></select></label>
        <label>毕业年份<input v-model="fields.graduation_year" type="number" min="2000" max="2100" placeholder="不限，含往届" list="graduation-years" /><datalist id="graduation-years"><option v-for="year in [2027, 2026, 2025, 2024, 2023]" :key="year" :value="year" /></datalist></label>
        <label>笔试<select v-model="fields.written_test"><option value="">全部笔试状态</option><option value="yes">有笔试</option><option value="no">官方明确无笔试</option><option value="unknown">笔试未公布</option></select></label>
        <label>截止日期不晚于<input v-model="fields.deadline_before" type="date" /></label>
        <label>公开状态<select v-model="fields.status"><option value="">招聘中 / 待确认</option><option value="open">公开招聘中</option><option value="unknown">状态待确认</option><option value="closed">已截止 / 关闭</option><option value="all">全部状态</option></select></label>
        <label class="checkbox-label"><input :checked="fields.include_unknown !== 'false'" type="checkbox" @change="fields.include_unknown = ($event.target as HTMLInputElement).checked ? 'true' : 'false'" /><span>也显示未公布专业 / 毕业范围的岗位</span></label>
        <p class="field-hint">筛选范围匹配不等于已满足个人投递资格。具体学历、地区和优先条件请看岗位原文。</p>
        <button class="button primary full">应用筛选<Icon name="arrow" /></button>
      </form>
    </aside>
    <section class="results-panel" aria-label="岗位结果">
      <form v-if="!isFavorites" class="search-bar" @submit.prevent="apply"><Icon name="search" /><input v-model="fields.q" aria-label="岗位关键词" maxlength="120" placeholder="搜索岗位、技术栈或工作内容，例如 Go、后端、基础设施" /><button class="button primary">搜索</button></form>
      <div v-if="!isFavorites" class="type-tabs"><button v-for="[key, label] in [['', '全部机会'], ['campus', '校园招聘'], ['internship', '实习'], ['early_career', '应届 / 低经验社招']]" :key="key" :class="{ active: fields.recruitment_type === key }" @click="fields.recruitment_type = key; apply()">{{ label }}</button></div>
      <div class="results-toolbar"><span>{{ isFavorites ? '本页收藏' : '符合筛选' }} <strong>{{ isFavorites ? page.items.length : page.total }}</strong> {{ isFavorites ? '条' : '个岗位广告' }}<small v-if="!isFavorites"> · 具体方向与条件见详情</small></span><div class="toolbar-controls"><button v-if="!isFavorites" class="button ghost mobile-filter" @click="filterOpen = true"><Icon name="filter" />筛选</button><select v-if="!isFavorites" v-model="fields.sort" aria-label="岗位排序" @change="apply"><option value="updated">最近更新</option><option value="deadline">截止日期</option></select><div class="view-toggle" aria-label="岗位视图"><button aria-label="列表视图" :aria-pressed="view === 'list'" @click="changeView('list')"><Icon name="list" /></button><button aria-label="表格视图" :aria-pressed="view === 'table'" @click="changeView('table')"><Icon name="table" /></button></div></div></div>
      <div v-if="error" class="empty-state" role="alert"><Icon name="shield" /><h2>暂时没有加载成功</h2><p>{{ error }}</p><button class="button secondary" @click="load">重新加载</button></div>
      <div v-else-if="loading" class="loading-state" role="status"><span class="spinner"></span>正在整理机会…</div>
      <div v-else-if="!page.items.length" class="empty-state"><Icon :name="isFavorites ? 'bookmark' : 'search'" /><h2>{{ isFavorites ? '还没有收藏的机会' : '暂时没有匹配的岗位' }}</h2><p>{{ isFavorites ? '在机会看板点亮收藏，方便之后再看。' : '可以放宽筛选条件，或稍后查看来源更新。' }}</p><RouterLink v-if="isFavorites" class="button primary" to="/jobs">去看看机会</RouterLink><button v-else class="button secondary" @click="reset">清除筛选</button></div>
      <div v-else :class="['job-results', { 'table-view': view === 'table' }]">
        <div v-if="view === 'table'" class="job-table-head"><span>公司 / 岗位</span><span>方向 / 地点</span><span>类型 / 笔试</span><span>截止 / 核验</span><span>操作</span></div>
        <article v-for="job in page.items" :key="job.id" class="job-card">
          <div class="job-identity"><span class="company-mark" :class="job.company_code">{{ job.company_name.slice(0, 1) }}</span><div><span class="company-name">{{ job.company_name }} <span class="tiny-separator">/</span> {{ job.campaign_name }}</span><h2><RouterLink :to="{ name: 'job', params: { id: job.id }, query: route.query }">{{ job.title }}</RouterLink></h2><span class="public-state" :class="job.public_status"><span class="status-dot"></span>{{ publicStatuses[job.public_status] }}<span v-if="job.verification_status !== 'verified'"> · 待重新核验</span></span></div><button class="icon-button save-button" :class="{ saved: favorites.has(job.id) }" :aria-label="favorites.has(job.id) ? '取消收藏 ' + job.title : '收藏 ' + job.title" :disabled="pending.has(job.id)" @click="favorite(job)"><Icon name="bookmark" /></button></div>
          <div class="job-places"><div class="tag-row"><span v-for="direction in [...new Set(variants(job).map(v => v.direction))]" :key="direction" class="tag">{{ directions[direction] }}</span><span v-if="job.variants.length > 1" class="tag subtle">{{ job.variants.length }} 个方向</span></div><span class="meta-line"><Icon name="location" />{{ places(job) }}</span></div>
          <div class="job-conditions"><span class="tag warm">{{ recruitmentTypes[job.recruitment_type] }}</span><span class="meta-line">{{ writtenTests[job.written_test] }}</span></div>
          <p class="job-preview">{{ variants(job)[0]?.responsibilities.slice(0, 140) }}</p>
          <div class="job-deadline"><span :class="{ muted: !job.deadline_date }"><Icon name="clock" />{{ job.deadline_date ? '截止 ' + job.deadline_date : '截止未公布' }}</span><small>核验于 {{ time(job.verified_at) }}{{ job.deadline_date && job.deadline_timezone === 'unknown' ? ' · 截止时区待确认' : '' }}</small></div>
          <div class="job-actions"><button class="button secondary compact" :disabled="pending.has(job.id)" @click="record(job)">{{ recorded.has(job.id) ? '查看进度' : '记录投递' }}</button><RouterLink class="text-link" :to="{ name: 'job', params: { id: job.id }, query: route.query }">查看详情<Icon name="arrow" /></RouterLink></div>
        </article>
      </div>
      <div v-if="page.items.length || page.offset" class="pagination"><span>第 {{ Math.floor(page.offset / 20) + 1 }} 页<span v-if="!isFavorites"> · 共 {{ page.total }} 个岗位广告</span></span><div><button class="button ghost compact" :disabled="!page.offset || loading" @click="paginate(-1)">上一页</button><button class="button ghost compact" :disabled="loading || (isFavorites ? page.items.length < 20 : page.offset + 20 >= page.total)" @click="paginate(1)">下一页</button></div></div>
      <p class="results-footnote"><Icon name="shield" />官方入口可能要求登录。未知信息会保留为待确认，记录投递不会代替官网申请。</p>
    </section>
  </div>
</template>
