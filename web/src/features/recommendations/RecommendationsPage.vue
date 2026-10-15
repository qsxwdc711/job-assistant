<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useSession } from '../../stores/session'
import { api } from '../../shared/api/client'
import type { Job, Page, ProfileVersion, Recommendation, User } from '../../shared/api/types'
import { errorText, time, directions, processingError } from '../../shared/display'
const route=useRoute(),session=useSession(),owner=ref(session.isAdmin?String(route.query.owner_user_id??''):''),users=ref<User[]>([])
const allowed=computed(()=>!!session.capabilities['recommendations.read'])
function path(suffix:string){return (owner.value&&session.isAdmin?'/admin':'/me')+suffix+(owner.value&&session.isAdmin?(suffix.includes('?')?'&':'?')+'owner_user_id='+encodeURIComponent(owner.value):'')}
const jobs=ref<Job[]>([]),selected=ref<string[]>(route.query.job_id?[String(route.query.job_id)]:[]),keyword=ref(''),items=ref<Recommendation[]>([]),profiles=ref<ProfileVersion[]>([])
const configured=ref(false),useModel=ref(true),busy=ref(false),error=ref(''),loading=ref(false),offset=ref(0),hasMore=ref(false)
const current=computed(()=>profiles.value.find(v=>v.current)),eligibility:Record<string,string>={eligible:'满足已知条件',needs_confirmation:'仍有条件待确认',ineligible:'不满足明确条件'}
const semanticStates:Record<string,string>={pending:'语义比较排队或处理中',not_configured:'模型未配置，仅展示规则',not_requested:'仅运行本地规则',not_applicable:'资格不满足，未调用模型',consent_required:'此资料版本未授权云端处理',stale:'资料或岗位已更新，请重新评估',failed:'语义比较失败，可重试',cancelled:'任务已取消'}
const levels:Record<string,string>={high:'高',medium:'中',low:'低'},groupNames={required:'必需条件的对应证据',preferred:'优先条件的对应证据',gaps:'资料中尚未找到的证据'}
let generation=0,polling=false
async function load(){if(!allowed.value)return;const g=generation;loading.value=true;error.value='';try{const [p,r]=await Promise.all([api<{items:ProfileVersion[]}>(path('/profiles')),api<{items:Recommendation[];model_configured:boolean}>(path('/recommendations?limit=20&offset='+offset.value))]);if(g!==generation||!allowed.value)return;profiles.value=p.items;items.value=r.items;configured.value=r.model_configured;hasMore.value=r.items.length===20}catch(e){if(g===generation)error.value=errorText(e)}finally{if(g===generation)loading.value=false}}
async function search(){try{const result=await api<Page<Job>>('/jobs?limit=20&q='+encodeURIComponent(keyword.value));jobs.value=result.items}catch(e){error.value=errorText(e)}}
async function generate(retry=false){if(!selected.value.length)return;busy.value=true;error.value='';try{await api(path('/recommendations'),{method:'POST',body:{job_ids:selected.value,use_model:useModel.value,retry}});offset.value=0;await load()}catch(e){error.value=errorText(e)}finally{busy.value=false}}
async function retry(item:Recommendation){busy.value=true;try{await api(path('/recommendations'),{method:'POST',body:{job_ids:[item.job_id],use_model:true,retry:true}});await load()}catch(e){error.value=errorText(e)}finally{busy.value=false}}
function variant(item:Recommendation){return item.job.variants.find(v=>v.key===item.variant_key)}
async function paginate(delta:number){offset.value=Math.max(0,offset.value+delta*20);await load()}
async function poll(){if(polling||!items.value.some(r=>r.semantic_status==='pending')||!allowed.value)return;polling=true;try{await load()}finally{polling=false}}
const timer=window.setInterval(()=>void poll(),3000)
onBeforeUnmount(()=>{generation++;clearInterval(timer)})
watch([owner,allowed],()=>{generation++;items.value=[];profiles.value=[];offset.value=0;void load()})
if(session.isAdmin)void api<{items:User[]}>('/admin/users?limit=100').then(r=>users.value=r.items).catch(e=>error.value=errorText(e))
void load();void search()
</script>
<template>
 <section class="page-heading"><div><span class="eyebrow">让依据帮助判断</span><h1>岗位匹配</h1><p>先核对资格，再比较经历与岗位方向。相关性高低不代表录用概率。</p></div><RouterLink class="button secondary" :to="{path:'/resumes',query:owner?{owner_user_id:owner}:{}}">管理简历资料</RouterLink></section>
 <label v-if="session.isAdmin" class="owner-picker">资料所属账号<select v-model="owner"><option value="">我的账号</option><option v-for="user in users.filter(u=>u.id!==session.user?.id)" :key="user.id" :value="user.id">{{ user.username }}</option></select></label>
 <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
 <section v-if="!allowed" class="panel empty-state"><h2>岗位匹配需要 VIP 权限</h2><p>你可以继续浏览公开岗位、收藏和管理自己的投递进度。</p></section>
 <template v-else>
  <section class="panel private-notice"><h2>当前资料</h2><p v-if="current">{{ time(current.created_at) }} · {{ current.candidate.skills?.join(' / ') || '未填写技能' }} · {{ current.cloud_approved?'已授权语义比较':'仅本地规则' }}</p><p v-else>请先上传简历，核对并保存一份当前资料。</p><p class="field-hint">{{ configured?'云端比较仅使用你已授权的脱敏资料。':'模型尚未配置，当前可验收本地资格规则。' }}</p></section>
  <section class="panel private-notice"><h2>选择要评估的岗位</h2><form class="recommendation-search" @submit.prevent="search"><label>搜索岗位<input v-model="keyword" maxlength="200" placeholder="岗位名称或公司" /></label><button class="button secondary">搜索</button></form><div class="job-choices"><label v-for="job in jobs" :key="job.id"><input v-model="selected" type="checkbox" :value="job.id" :disabled="!selected.includes(job.id)&&selected.length>=20" /><span><strong>{{ job.title }}</strong><small>{{ job.company_name }} · {{ job.campaign_name }}</small></span></label></div><p class="field-hint">展示搜索结果前 20 条，每次最多选择 20 个岗位；每个方向的条件与地点分别评估。</p><div class="recommendation-actions"><label class="consent-row"><input v-model="useModel" type="checkbox" />资格允许时，使用已授权资料进行语义比较</label><button class="button primary" :disabled="busy || !current || !selected.length" @click="generate(false)">{{ busy?'正在提交…':'评估所选岗位（'+selected.length+'）' }}</button></div></section>
  <div class="private-header"><h2>评估记录</h2><button class="button ghost compact" :disabled="loading" @click="load">刷新结果</button></div>
  <p v-if="!items.length" class="panel private-notice muted">{{ loading?'正在读取…':'还没有评估记录。选择岗位后即可开始。' }}</p>
  <article v-for="item in items" :key="item.id" class="panel recommendation-card"><header class="private-header"><div><small>{{ item.job.company_name }}</small><h2><RouterLink :to="{path:'/jobs/'+item.job_id,query:{variant:item.variant_key}}">{{ item.job.title }} · {{ variant(item)?.name || item.variant_key }}</RouterLink></h2><p class="field-hint">{{ directions[variant(item)?.direction ?? ''] ?? '' }} · {{ variant(item)?.locations.map(p=>p.city).join(' / ') || '地点未公布' }}</p></div><span class="tag" :class="{warning:item.eligibility.status!=='eligible'}">{{ eligibility[item.eligibility.status] ?? item.eligibility.status }}</span></header><details class="qualification-checks" open><summary>资格检查依据</summary><ul><li v-for="(check,i) in item.eligibility.checks" :key="i"><strong>{{ eligibility[check.status] }}</strong> · {{ check.reason }}<blockquote v-if="check.evidence">{{ check.evidence }}</blockquote></li></ul></details>
   <div class="semantic-result"><h3>语义相关性：{{ item.semantic_status==='succeeded'?levels[item.semantic.level]:'暂未生成' }}</h3><p v-if="item.semantic_status!=='succeeded'" class="field-hint">{{ semanticStates[item.semantic_status] ?? item.semantic_status }}<span v-if="item.error_code">（{{ processingError(item.error_code) }}）</span></p><template v-else><section v-for="(label,key) in groupNames" :key="key" class="citation-group"><h4>{{ label }}</h4><p v-if="!item.semantic[key]?.length" class="field-hint">暂无对应条目。</p><div v-for="(citation,index) in item.semantic[key]" :key="index" class="citation"><p>{{ citation.statement }}</p><blockquote v-if="citation.resume_quote"><small>资料原文</small>{{ citation.resume_quote }}</blockquote><blockquote><small>岗位原文</small>{{ citation.jd_quote }}</blockquote></div></section></template><button v-if="['failed','cancelled','not_configured'].includes(item.semantic_status)&&configured" class="button secondary compact" :disabled="busy" @click="retry(item)">重试语义比较</button></div><p class="field-hint">评估于 {{ time(item.created_at) }}。{{ item.semantic_status==='stale'?'旧结果不再作为当前结论。':'未找到资料证据的条件仍需核实。' }}</p>
  </article><div class="pagination"><button class="button ghost" :disabled="!offset||loading" @click="paginate(-1)">上一页</button><span>第 {{ offset/20+1 }} 页</span><button class="button ghost" :disabled="!hasMore||loading" @click="paginate(1)">下一页</button></div>
 </template>
</template>
