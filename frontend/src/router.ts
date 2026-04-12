import { createRouter, createWebHistory } from 'vue-router'
import RatesPage from './pages/RatesPage.vue'
import PlannerPage from './pages/PlannerPage.vue'
import HistoryPage from './pages/HistoryPage.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/rates' },
    { path: '/rates', component: RatesPage },
    { path: '/planner', component: PlannerPage },
    { path: '/history', component: HistoryPage },
  ],
})
