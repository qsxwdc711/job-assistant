<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../../shared/api/client'
import type { Job } from '../../shared/api/types'
import { directions, publicStatuses, writtenTests, recruitmentTypes, time, notify, errorText } from '../../shared/display'
import Icon from '../../shared/Icon.vue'
const route = useRoute(), router = useRouter(), job = ref<Job | null>(null), selected = ref(''), error = ref(''), busy = ref(false)
const variant = computed(() => job.value?.variants.find(v => v.key === selected.value) ?? job.value?.variants[0])
const regions: Record<string, string> = { domestic: '境内', overseas: '海外', all: '境内 / 海外' }
const education: Record<string, string> = { bachelor_master: '本科 / 硕士', doctorate: '博士', bachelor_master_doctorate: '本硕博', unknown: '学历以岗位为准' }
const linkLevels: Record<string, string> = { job: '岗位详情', campaign: '招聘批次', company: '公司入口' }
watch(() => route.params.id, async () => {
  error.value = ''; job.value = null
  try { const result = await api<{job: Job}>('/jobs/' + encodeURIComponent(String(route.params.id))); job.value = result.job; const direction = String(route.query.direction ?? ''), city = String(route.query.city ?? ''); selected.value = result.job.variants.find(v => v.key === route.query.variant)?.key ?? result.job.variants.find(v => (!direction || v.direction === direction) && (!city || v.locations.some(p => p.code === city || p.city === city || p.kind === city)))?.key ?? result.job.variants[0]?.key ?? '' }
  catch (value) { error.value = errorText(value) }
}, { immediate: true })
async function record() { if (!job.value) return; busy.value = true; try { await api('/me/applications', { method: 'POST', body: { job_id: job.value.id, status: 'draft' } }); notify('已记录为待投递，实际申请请通过官方入口完成。') } catch (value) { notify(errorText(value), 'error') } finally { busy.value = false } }
async function save() { if (!job.value) return; try { await api('/me/favorites/' + job.value.id, { method: 'PUT' }); notify('已加入收藏') } catch (value) { notify(errorText(value), 'error') } }
</script>
<template>
  <RouterLink class="back-link" :to="{ path: '/jobs', query: route.query }">← 返回机会看板</RouterLink>
  <div v-if="error" class="empty-state" role="alert"><h2>暂时无法查看这个岗位</h2><p>{{ error }}</p></div>
  <div v-else-if="!job" class="loading-state"><span class="spinner"></span>正在读取岗位…</div>
  <div v-else class="detail-layout">
    <div class="detail-main">
      <section class="detail-header panel"><div class="tag-row"><span class="tag warm">{{ recruitmentTypes[job.recruitment_type] }}</span><span class="public-state" :class="job.public_status">{{ publicStatuses[job.public_status] }}</span><span v-if="job.verification_status !== 'verified'" class="tag warning">需要重新核验</span></div><h1>{{ job.title }}</h1><p class="detail-company">{{ job.company_name }} <span>／</span> {{ job.campaign_name }}</p><div class="detail-meta"><span><Icon name="location" />{{ variant?.locations.map(p => p.city).join(' / ') || '地点未公布' }}</span><span><Icon name="clock" />核验于 {{ time(job.verified_at) }}</span></div><div class="detail-actions"><a class="button primary" :href="job.apply_url" target="_blank" rel="noopener noreferrer">前往官方投递<Icon name="external" /></a><button class="button secondary" :disabled="busy" @click="record">记录投递</button><button class="button ghost" @click="save"><Icon name="bookmark" />收藏</button></div><p class="field-hint">{{ job.login_required ? '官网申请需要登录。' : '' }}这里的“记录投递”只管理个人进度，不会自动提交简历。</p></section>
      <section class="panel detail-body"><h2>选择岗位方向</h2><p class="muted">每个方向的要求和地点分别保留。请以你选中的方向为准。</p><div class="variant-tabs" role="tablist" aria-label="岗位方向"><button v-for="item in job.variants" :key="item.key" role="tab" :aria-selected="selected === item.key" :class="{ active: selected === item.key }" @click="selected = item.key">{{ item.name }}</button></div><template v-if="variant"><div class="section-label"><span class="tag">{{ directions[variant.direction] }}</span><span class="meta-line"><Icon name="location" />{{ variant.locations.map(p => p.city).join(' / ') || '地点未公布' }}</span></div><h3>工作职责</h3><p class="job-text">{{ variant.responsibilities || '官方未公布' }}</p><h3>岗位要求</h3><p class="job-text">{{ variant.requirements || '官方未公布' }}</p><template v-if="variant.preferred"><h3>官方列明的加分项</h3><p class="job-text">{{ variant.preferred }}</p></template><details v-if="variant.departments?.length" class="department-details"><summary>查看该方向的部门与地点关系</summary><div v-for="(department, index) in variant.departments" :key="department.code + ':' + index" class="department-row"><strong>{{ department.name || '部门名称未公布' }}</strong><span>{{ department.locations.map(p => p.city).join(' / ') || '地点未明确' }}</span></div></details></template></section>
    </div>
    <aside class="detail-aside">
      <section class="panel"><h2>投递前，再确认</h2><dl class="fact-list"><dt>截止日期</dt><dd>{{ job.deadline_date || '未公布' }}<small v-if="job.deadline_date && job.deadline_timezone === 'unknown'">仅公布日期，时区未确认</small></dd><dt>笔试</dt><dd>{{ writtenTests[job.written_test] }}<small v-if="job.written_test_evidence">{{ job.written_test_evidence }}</small></dd><dt>在校身份</dt><dd>{{ job.student_required === 'yes' ? '明确要求在校生' : '岗位未明确' }}</dd><dt>岗位最低学历</dt><dd>{{ job.minimum_education === 'bachelor_or_above' ? '本科及以上' : '以所选方向的岗位要求为准' }}</dd><template v-if="job.minimum_internship_months"><dt>最短实习时长</dt><dd>{{ job.minimum_internship_months }} 个月</dd></template><template v-if="job.days_per_week"><dt>每周出勤</dt><dd>{{ job.days_per_week }} 天</dd></template><template v-if="job.graduation_preference"><dt>届数优先条件</dt><dd>{{ job.graduation_preference }}<small>“优先”不等于只接受该届。</small></dd></template></dl><div v-if="job.graduation_rules?.length" class="graduation-evidence"><h3>适用毕业范围</h3><p v-for="(rule, index) in job.graduation_rules" :key="index"><strong>{{ regions[rule.region] ?? rule.region }} · {{ education[rule.education] ?? rule.education }}</strong><span>{{ rule.from }} 至 {{ rule.through }}</span><small>{{ rule.evidence }}</small></p></div><p v-else class="field-hint">毕业时间范围未明确，请结合官方原文确认。未披露的信息不代表不限条件。</p></section>
      <section class="panel evidence-panel"><span class="evidence-icon"><Icon name="shield" /></span><h2>官方溯源</h2><p>岗位职责和条件来自官方公开页面。资格是否满足，仍需结合个人情况与官网申请规则确认。</p><a class="text-link" :href="job.source_url" target="_blank" rel="noopener noreferrer">查看官方出处<Icon name="external" /></a><small>链接级别：{{ linkLevels[job.apply_level] ?? '官方入口' }}<br>招聘主体：{{ job.publisher }}</small><details><summary>查看批次依据</summary><p class="job-text">{{ job.campaign_evidence }}</p></details></section>
    </aside>
  </div>
</template>
