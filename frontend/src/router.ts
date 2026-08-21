import { createRouter, createWebHistory } from 'vue-router'
import RatesPage from './pages/RatesPage.vue'
import PlannerPage from './pages/PlannerPage.vue'
import HistoryPage from './pages/HistoryPage.vue'
import UsageHistoryPage from './pages/UsageHistoryPage.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/rates' },
    { path: '/rates', component: RatesPage },
    { path: '/usage-history', component: UsageHistoryPage },
    { path: '/planner', component: PlannerPage },
    { path: '/history', component: HistoryPage },
  ],
})
