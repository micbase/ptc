<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { fetchPlans, fetchChartData, fetchLatestDate } from './api'
import type { ElectricityRate, ChartPoint } from './types'
import RateChart from './components/RateChart.vue'
import PlansTable from './components/PlansTable.vue'

const date = ref(new Date().toISOString().slice(0, 10))
const plans = ref<ElectricityRate[]>([])
const best = ref<ChartPoint[]>([])
const best3m = ref<ChartPoint[]>([])
const variable = ref<ChartPoint[]>([])
const loading = ref(false)
const chartLoading = ref(false)

async function loadPlans() {
  loading.value = true
  try {
    plans.value = await fetchPlans(date.value)
  } catch (e) {
    console.error('Failed to load plans:', e)
    plans.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  // Load charts independently
  chartLoading.value = true
  Promise.all([
    fetchChartData('best'),
    fetchChartData('best_3m'),
    fetchChartData('variable'),
  ]).then(([b, b3, v]) => {
    best.value = b
    best3m.value = b3
    variable.value = v
  }).catch(e => console.error('Failed to load chart data:', e))
  .finally(() => { chartLoading.value = false })

  // Load latest date, then plans
  fetchLatestDate().then(d => {
    date.value = d
    // watch will trigger loadPlans
  }).catch(() => {
    // Fallback to today
    loadPlans()
  })
})

watch(date, loadPlans)
</script>

<template>
  <div class="w-full px-4 py-6">
    <h1 class="text-2xl font-bold text-gray-900 mb-6">Power to Choose — ONCOR Rate Comparison</h1>

    <div class="bg-white rounded-lg shadow p-4 mb-6">
      <RateChart :best="best" :best3m="best3m" :variable="variable" :loading="chartLoading" />
    </div>

    <div class="flex items-center gap-4 mb-4">
      <label class="text-sm font-medium text-gray-700">Date:</label>
      <input
        v-model="date"
        type="date"
        class="border border-gray-300 rounded px-3 py-1.5 text-sm"
      />
      <span class="text-sm text-gray-500">{{ loading ? '' : `${plans.length} plans` }}</span>
    </div>

    <div class="bg-white rounded-lg shadow">
      <PlansTable :plans="plans" :loading="loading" />
    </div>
  </div>
</template>
