import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from './stores/session'
import LoginPage from './features/account/LoginPage.vue'
import JobsPage from './features/jobs/JobsPage.vue'
import JobDetail from './features/jobs/JobDetail.vue'
import ApplicationsPage from './features/applications/ApplicationsPage.vue'
import AdminPage from './features/admin/AdminPage.vue'
import ResumePage from './features/resumes/ResumePage.vue'
import RecommendationsPage from './features/recommendations/RecommendationsPage.vue'
import NotificationsPage from './features/notifications/NotificationsPage.vue'
export const router = createRouter({ history: createWebHistory(), routes: [
  { path: '/', redirect: '/jobs' }, { path: '/login', name: 'login', component: LoginPage },
  { path: '/jobs', name: 'jobs', component: JobsPage }, { path: '/jobs/:id', name: 'job', component: JobDetail },
  { path: '/favorites', name: 'favorites', component: JobsPage },
  { path: '/applications', name: 'applications', component: ApplicationsPage },
  { path: '/resumes', name: 'resumes', component: ResumePage },
  { path: '/recommendations', name: 'recommendations', component: RecommendationsPage },
  { path: '/notifications', name: 'notifications', component: NotificationsPage },
  { path: '/admin', name: 'admin', component: AdminPage, meta: { admin: true } },
  { path: '/:pathMatch(.*)*', redirect: '/jobs' },
] })
router.beforeEach(async to => {
  const session = useSession()
  if (!session.initialized) await session.refresh()
  if (to.name !== 'login' && !session.user) return { name: 'login', query: { redirect: to.fullPath } }
  if (to.meta.admin && !session.isAdmin) return { name: 'jobs' }
  if (to.name === 'login' && session.user) return { name: 'jobs' }
})
