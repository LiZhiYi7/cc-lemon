import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from '@/stores/session'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('@/views/Login.vue'), meta: { public: true } },
    { path: '/', redirect: '/books' },
    { path: '/books', component: () => import('@/views/Books.vue') },
    { path: '/users', component: () => import('@/views/Users.vue') },
    {
      path: '/b/:bookId',
      component: () => import('@/views/Layout.vue'),
      children: [
        { path: '', redirect: (to) => `/b/${to.params.bookId}/vouchers` },
        { path: 'vouchers', component: () => import('@/views/voucher/VoucherList.vue') },
        { path: 'vouchers/new', component: () => import('@/views/voucher/VoucherEdit.vue') },
        { path: 'vouchers/:voucherId', component: () => import('@/views/voucher/VoucherEdit.vue') },
        { path: 'ledger/trial', component: () => import('@/views/ledger/TrialBalance.vue') },
        { path: 'ledger/detail', component: () => import('@/views/ledger/Detail.vue') },
        { path: 'ledger/general', component: () => import('@/views/ledger/General.vue') },
        { path: 'reports/:code', component: () => import('@/views/report/Report.vue') },
        { path: 'closing', component: () => import('@/views/closing/Closing.vue') },
        { path: 'settings/accounts', component: () => import('@/views/settings/Accounts.vue') },
        { path: 'settings/opening', component: () => import('@/views/settings/Opening.vue') },
        { path: 'settings/parallel', component: () => import('@/views/settings/ParallelRules.vue') },
        { path: 'settings/book', component: () => import('@/views/settings/BookSettings.vue') },
      ],
    },
    { path: '/:rest(.*)*', redirect: '/books' },
  ],
})

router.beforeEach(async (to) => {
  const session = useSession()
  if (to.meta.public) return true
  if (!session.token) return { path: '/login', query: { redirect: to.fullPath } }
  try {
    await session.ensureUser()
  } catch {
    session.logout()
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  return true
})

export default router
