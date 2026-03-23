<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { fetchPlans, fetchChartData, fetchLatestDate, triggerFetch } from './api'
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
const fetching = ref(false)
const fetchMessage = ref('')
const fetchError = ref('')

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

async function onFetch(force = false) {
  fetching.value = true
  fetchMessage.value = ''
  fetchError.value = ''
  try {
    const result = await triggerFetch(force)
    fetchMessage.value = result.message
    if (!result.skipped) {
      // Reload charts and plans after new data inserted
      await loadPlans()
      const [b, b3, v] = await Promise.all([
        fetchChartData('best'),
        fetchChartData('best_3m'),
        fetchChartData('variable'),
      ])
      best.value = b
      best3m.value = b3
      variable.value = v
    }
  } catch (e: any) {
    fetchError.value = e.message ?? 'Fetch failed'
  } finally {
    fetching.value = false
  }
}
</script>

<template>
  <div class="w-full px-4 py-6">
    <h1 class="text-2xl font-bold text-gray-900 mb-6">Power to Choose — ONCOR Rate Comparison</h1>

    <div class="bg-white rounded-lg shadow p-4 mb-6">
      <RateChart :best="best" :best3m="best3m" :variable="variable" :loading="chartLoading" />
    </div>

    <div class="flex items-center gap-4 mb-4 flex-wrap">
      <label class="text-sm font-medium text-gray-700">Date:</label>
      <input
        v-model="date"
        type="date"
        class="border border-gray-300 rounded px-3 py-1.5 text-sm"
      />
      <span class="text-sm text-gray-500">{{ loading ? '' : `${plans.length} plans` }}</span>
      <div class="ml-auto flex items-center gap-2">
        <button
          @click="onFetch(false)"
          :disabled="fetching"
          class="px-3 py-1.5 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {{ fetching ? 'Fetching…' : 'Fetch Today\'s Data' }}
        </button>
        <button
          @click="onFetch(true)"
          :disabled="fetching"
          class="px-3 py-1.5 text-sm font-medium rounded border border-gray-300 text-gray-700 hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Force Refresh
        </button>
      </div>
    </div>
    <div v-if="fetchMessage" class="mb-3 text-sm text-green-700 bg-green-50 border border-green-200 rounded px-3 py-2">
      {{ fetchMessage }}
    </div>
    <div v-if="fetchError" class="mb-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
      {{ fetchError }}
    </div>

    <div class="bg-white rounded-lg shadow">
      <PlansTable :plans="plans" :loading="loading" />
    </div>
  </div>
</template>
