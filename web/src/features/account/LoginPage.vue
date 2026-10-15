<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSession } from '../../stores/session'
import { errorText } from '../../shared/display'
import Icon from '../../shared/Icon.vue'
const username = ref(''), password = ref(''), busy = ref(false), error = ref('')
const session = useSession(), router = useRouter(), route = useRoute()
async function submit() {
  error.value = ''; busy.value = true
  try { await session.login(username.value.trim(), password.value); password.value = ''; const redirect = String(route.query.redirect ?? '/jobs'); await router.replace(redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/jobs') }
  catch (value) { error.value = errorText(value) } finally { busy.value = false }
}
</script>
<template>
  <section class="login-story"><div class="brand"><span class="brand-symbol"><Icon name="grid" /></span><span>职序<small>JOB ASSISTANT</small></span></div><div class="login-story-body"><span class="eyebrow">让每一步，都有迹可循</span><h1>下一份机会，<br>从这里开始。</h1><p>查看有官方出处的岗位，留下自己的投递进度。<br>电脑和手机，共用一份清晰的求职记录。</p><div class="story-features"><span><Icon name="shield" />官方溯源</span><span><Icon name="filter" />组合筛选</span><span><Icon name="briefcase" />进度管理</span></div></div><small class="login-footnote">认真对待每一次选择。</small></section>
  <section class="login-form-side"><form class="login-form" @submit.prevent="submit"><span class="eyebrow">欢迎回来</span><h2>登录你的工作台</h2><p class="muted">使用管理员为你开通的账号登录。</p><label>账号<input v-model="username" required maxlength="40" autocomplete="username" placeholder="请输入账号" /></label><label>密码<input v-model="password" required type="password" autocomplete="current-password" placeholder="请输入密码" /></label><p v-if="error" class="inline-error" role="alert">{{ error }}</p><button class="button primary full" :disabled="busy">{{ busy ? '正在登录…' : '登录' }}<Icon name="arrow" /></button><p class="login-help">暂无账号或忘记密码？请联系管理员。</p></form></section>
</template>
