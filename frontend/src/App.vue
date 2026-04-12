<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { fetchPlans, fetchChartData, fetchLatestDate, triggerFetch } from './api'
import type { ElectricityRate, ChartPoint } from './types'
import RateChart from './components/RateChart.vue'
import PlansTable from './components/PlansTable.vue'
import SwitchPlanner from './components/SwitchPlanner.vue'
import SwitchHistory from './components/SwitchHistory.vue'

type Tab = 'rates' | 'planner' | 'history'
const activeTab = ref<Tab>('rates')

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
  <div class="w-full">
    <!-- Tab navigation -->
    <div class="border-b border-gray-200 bg-white px-4">
      <nav class="flex gap-1 -mb-px">
        <button
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            activeTab === 'rates'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
          @click="activeTab = 'rates'"
        >
          Rate Comparison
        </button>
        <button
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            activeTab === 'planner'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
          @click="activeTab = 'planner'"
        >
          Switch Planner
        </button>
        <button
          :class="[
            'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
            activeTab === 'history'
              ? 'border-blue-600 text-blue-700'
              : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300',
          ]"
          @click="activeTab = 'history'"
        >
          Switch History
        </button>
      </nav>
    </div>

    <!-- Rate Comparison tab -->
    <div v-show="activeTab === 'rates'" class="px-4 py-6">
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
        <div class="ml-auto">
          <button
            @click="onFetch()"
            :disabled="fetching"
            class="px-3 py-1.5 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {{ fetching ? 'Fetching…' : 'Fetch Today\'s Data' }}
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

    <!-- Switch Planner tab -->
    <div v-show="activeTab === 'planner'">
      <SwitchPlanner />
    </div>

    <!-- Switch History tab -->
    <div v-show="activeTab === 'history'">
      <SwitchHistory />
    </div>
  </div>
</template>
