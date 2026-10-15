<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSession } from './stores/session'
import { notice, notify, errorText } from './shared/display'
import Icon from './shared/Icon.vue'
const session = useSession(), route = useRoute(), router = useRouter()
const roleNames: Record<string, string> = { admin: '管理员', vip: 'VIP', standard: '普通用户' }
const label = computed(() => roleNames[session.role] ?? '用户')
const navigation = computed(() => [ { to: '/jobs', label: '机会看板', icon: 'grid' }, { to: '/applications', label: '投递管理', icon: 'briefcase' }, { to: '/favorites', label: '我的收藏', icon: 'bookmark' }, ...(session.isAdmin ? [{ to: '/admin', label: '管理中心', icon: 'shield' }] : []) ])
const timer = window.setInterval(() => { if (session.user) void session.refresh() }, 60000)
onBeforeUnmount(() => clearInterval(timer))
watch(() => session.isAdmin, value => { if (!value && route.meta.admin) void router.replace('/jobs') })
async function logout() { try { await session.logout(); await router.replace('/login') } catch (error) { notify(errorText(error), 'error') } }
</script>
<template>
  <div v-if="route.name === 'login'" class="login-layout"><RouterView /></div>
  <div v-else class="app-layout">
    <aside class="sidebar">
      <RouterLink class="brand" to="/jobs"><span class="brand-symbol"><Icon name="grid" /></span><span>职序<small>JOB ASSISTANT</small></span></RouterLink>
      <div class="sidebar-label">你的求职工作台</div>
      <nav aria-label="主导航"><RouterLink v-for="item in navigation" :key="item.to" :to="item.to"><Icon :name="item.icon" /><span>{{ item.label }}</span><Icon class="nav-arrow" name="chevron" /></RouterLink></nav>
      <div class="sidebar-note"><span class="status-dot"></span>官方来源，保留依据<p>把机会看清楚，把进度记下来。</p></div>
      <div class="account"><span class="avatar">{{ session.user?.username.slice(0, 1).toUpperCase() }}</span><span class="account-name">{{ session.user?.username }}<small>{{ label }}</small></span><button class="icon-button" aria-label="退出登录" title="退出登录" @click="logout"><Icon name="logout" /></button></div>
    </aside>
    <div class="workspace">
      <header class="topbar"><span class="topbar-brand">职序 <span>／</span> {{ route.name === 'applications' ? '投递管理' : route.name === 'favorites' ? '我的收藏' : route.name === 'admin' ? '管理中心' : '机会看板' }}</span><span class="topbar-right"><span class="status-dot"></span> 个人求职工作台 <span class="role-badge">{{ label }}</span></span></header>
      <main class="main-content"><RouterView /></main>
      <nav class="mobile-nav" aria-label="手机导航"><RouterLink v-for="item in navigation" :key="item.to" :to="item.to"><Icon :name="item.icon" /><span>{{ item.label }}</span></RouterLink><button aria-label="退出登录" @click="logout"><Icon name="logout" /><span>退出</span></button></nav>
    </div>
  </div>
  <div v-if="notice.text" class="toast" :class="notice.kind" role="status"><Icon :name="notice.kind === 'error' ? 'shield' : 'check'" />{{ notice.text }}<button class="icon-button" aria-label="关闭提示" @click="notice.text = ''"><Icon name="close" /></button></div>
</template>
