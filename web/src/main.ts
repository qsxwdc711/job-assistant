import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { useSession } from './stores/session'
import { configureApi } from './shared/api/client'
import './style.css'
const app = createApp(App), pinia = createPinia()
app.use(pinia)
const session = useSession(pinia)
configureApi({ token: () => session.csrf, unauthorized: () => { session.clear(); if (router.currentRoute.value.name !== 'login') void router.replace({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } }) }, forbidden: () => { void session.refresh() } })
app.use(router)
app.mount('#app')
