<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { fetchPlans, fetchChartData, fetchLatestDate, triggerFetch, fetchUsageMonthly, fetchUsageAvg } from './api'
import type { ElectricityRate, ChartPoint, UsageMonth } from './types'
import RateChart from './components/RateChart.vue'
import PlansTable from './components/PlansTable.vue'
import UsageSummary from './components/UsageSummary.vue'

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

// Usage data
const usageMonths = ref<UsageMonth[]>([])
const avgKwh = ref(0)
const usageLoading = ref(false)
const userKwh = ref(0)

// Months left on current contract (persisted)
const monthsRemaining = ref(parseInt(localStorage.getItem('ptc_months_remaining') ?? '0') || 0)
watch(monthsRemaining, v => localStorage.setItem('ptc_months_remaining', String(v)))

// Current plan id_key (persisted)
const currentPlanIdKey = ref(localStorage.getItem('ptc_current_plan_id') ?? '')
watch(currentPlanIdKey, v => localStorage.setItem('ptc_current_plan_id', v))

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

  // Load usage data
  usageLoading.value = true
  Promise.all([fetchUsageMonthly(), fetchUsageAvg()])
    .then(([months, avg]) => {
      usageMonths.value = months
      avgKwh.value = avg.avg_monthly_kwh
      if (userKwh.value === 0 && avg.avg_monthly_kwh > 0) {
        userKwh.value = avg.avg_monthly_kwh
      }
    })
    .catch(e => console.error('Failed to load usage data:', e))
    .finally(() => { usageLoading.value = false })

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

async function onFetch() {
  fetching.value = true
  fetchMessage.value = ''
  fetchError.value = ''
  try {
    const result = await triggerFetch()
    fetchMessage.value = result.message
    const today = new Date().toISOString().slice(0, 10)
    if (date.value === today) {
      await loadPlans() // watch won't fire since date didn't change
    } else {
      date.value = today // watch triggers loadPlans
    }
    const [b, b3, v] = await Promise.all([
      fetchChartData('best'),
      fetchChartData('best_3m'),
      fetchChartData('variable'),
    ])
    best.value = b
    best3m.value = b3
    variable.value = v
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

    <div class="bg-white rounded-lg shadow p-4 mb-6">
      <UsageSummary :months="usageMonths" :avg-kwh="avgKwh" :loading="usageLoading" />
    </div>

    <div class="bg-white rounded-lg shadow p-4 mb-4">
      <div class="flex flex-wrap gap-4 items-end">
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">Date</label>
          <input
            v-model="date"
            type="date"
            class="border border-gray-300 rounded px-3 py-1.5 text-sm"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">Your monthly usage (kWh)</label>
          <input
            v-model.number="userKwh"
            type="number"
            min="0"
            step="1"
            class="border border-gray-300 rounded px-3 py-1.5 text-sm w-32"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">Months left on current contract</label>
          <input
            v-model.number="monthsRemaining"
            type="number"
            min="0"
            step="1"
            class="border border-gray-300 rounded px-3 py-1.5 text-sm w-24"
          />
        </div>
        <div class="flex items-center gap-3 ml-auto">
          <span class="text-sm text-gray-500">{{ loading ? '' : `${plans.length} plans` }}</span>
          <button
            @click="onFetch()"
            :disabled="fetching"
            class="px-3 py-1.5 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {{ fetching ? 'Fetching…' : 'Fetch Today\'s Data' }}
          </button>
        </div>
      </div>
    </div>

    <div v-if="fetchMessage" class="mb-3 text-sm text-green-700 bg-green-50 border border-green-200 rounded px-3 py-2">
      {{ fetchMessage }}
    </div>
    <div v-if="fetchError" class="mb-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
      {{ fetchError }}
    </div>

    <div class="bg-white rounded-lg shadow">
      <PlansTable
        :plans="plans"
        :loading="loading"
        :user-kwh="userKwh"
        :months-remaining="monthsRemaining"
        v-model:currentPlanIdKey="currentPlanIdKey"
      />
    </div>
  </div>
</template>
