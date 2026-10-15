import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, ApiError } from '../shared/api/client'
import type { Identity, User } from '../shared/api/types'
export const useSession = defineStore('session', () => {
  const user = ref<User | null>(null), role = ref(''), capabilities = ref<Record<string, boolean>>({}), csrf = ref(''), initialized = ref(false)
  const isAdmin = computed(() => role.value === 'admin' && !!capabilities.value['catalog.manage'])
  function assign(result: Identity) { user.value = result.user; role.value = result.effective_role; capabilities.value = result.capabilities; csrf.value = result.csrf_token; initialized.value = true }
  function clear() { user.value = null; role.value = ''; capabilities.value = {}; csrf.value = ''; initialized.value = true }
  async function refresh() { try { assign(await api<Identity>('/auth/me', { silentAuth: true })) } catch (error) { if (error instanceof ApiError && error.status === 401) clear(); else initialized.value = true } }
  async function login(username: string, password: string) { try { assign(await api<Identity>('/auth/login', { method: 'POST', body: { username, password }, silentAuth: true })) } catch (error) { if (error instanceof ApiError && error.status === 401) throw new ApiError(401, 'invalid_credentials'); throw error } }
  async function logout() { await api('/auth/logout', { method: 'POST' }); clear() }
  return { user, role, capabilities, csrf, initialized, isAdmin, clear, refresh, login, logout }
})
