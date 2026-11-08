<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  BarElement,
  BarController,
  CategoryScale,
  LinearScale,
  Tooltip,
  Legend,
} from 'chart.js'
import { fetchUsageHistory } from '../api'
import type { UsageDayPoint } from '../types'

ChartJS.register(BarElement, BarController, CategoryScale, LinearScale, Tooltip, Legend)

function today(): string {
  return new Date().toISOString().slice(0, 10)
}

function daysAgo(n: number): string {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d.toISOString().slice(0, 10)
}

function monthsAgoStart(n: number): string {
  const d = new Date()
  d.setDate(1)
  d.setMonth(d.getMonth() - n)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-01`
}

type ViewMode = 'daily' | 'monthly'

interface UsageRow {
  label: string
  actualKwh: number
  estimatedKwh: number
  days: number
  estimatedDays: number
}

const viewMode = ref<ViewMode>('daily')
const startDate = ref(daysAgo(30))
const endDate = ref(today())
const points = ref<UsageDayPoint[]>([])
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    points.value = await fetchUsageHistory(startDate.value, endDate.value)
  } catch (e: any) {
    error.value = e.message ?? 'Failed to load usage data'
  } finally {
    loading.value = false
  }
}

onMounted(load)

function setViewMode(mode: ViewMode) {
  if (mode === viewMode.value) return
  viewMode.value = mode
  startDate.value = mode === 'monthly' ? monthsAgoStart(11) : daysAgo(30)
  endDate.value = today()
  load()
}

const rows = computed<UsageRow[]>(() => {
  if (viewMode.value === 'daily') {
    return points.value.map((p) => ({
      label: p.date,
      actualKwh: p.is_actual ? p.kwh : 0,
      estimatedKwh: p.is_actual ? 0 : p.kwh,
      days: 1,
      estimatedDays: p.is_actual ? 0 : 1,
    }))
  }
  const byMonth = new Map<string, UsageRow>()
  for (const p of points.value) {
    const month = p.date.slice(0, 7)
    let row = byMonth.get(month)
    if (!row) {
      row = { label: month, actualKwh: 0, estimatedKwh: 0, days: 0, estimatedDays: 0 }
      byMonth.set(month, row)
    }
    row.days++
    if (p.is_actual) {
      row.actualKwh += p.kwh
    } else {
      row.estimatedKwh += p.kwh
      row.estimatedDays++
    }
  }
  return [...byMonth.values()].sort((a, b) => a.label.localeCompare(b.label))
})

function daysInMonth(month: string): number {
  const [y, m] = month.split('-').map(Number)
  return new Date(y, m, 0).getDate()
}

const totalKwh = computed(() => points.value.reduce((s, p) => s + p.kwh, 0))
const avgDailyKwh = computed(() =>
  points.value.length > 0 ? totalKwh.value / points.value.length : 0,
)
const avgMonthlyKwh = computed(() =>
  rows.value.length > 0 ? totalKwh.value / rows.value.length : 0,
)
const estimatedDays = computed(() => points.value.filter((p) => !p.is_actual).length)

const chartData = computed(() => ({
  labels: rows.value.map((r) => r.label),
  datasets: [
    {
      label: 'Actual (kWh)',
      data: rows.value.map((r) => r.actualKwh),
      backgroundColor: '#3b82f6',
      borderWidth: 0,
      borderSkipped: false,
      stack: 'usage',
    },
    {
      label: 'Estimated (kWh)',
      data: rows.value.map((r) => r.estimatedKwh),
      backgroundColor: '#f59e0b',
      borderWidth: 0,
      borderSkipped: false,
      stack: 'usage',
    },
  ],
}))

const chartOptions = computed(() => ({
  animation: false as const,
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    mode: 'index' as const,
    intersect: false,
  },
  scales: {
    x: {
      stacked: true,
      ticks: {
        maxRotation: 45,
        autoSkip: true,
        maxTicksLimit: 20,
      },
    },
    y: {
      stacked: true,
      title: { display: true, text: 'kWh' },
      beginAtZero: true,
    },
  },
  plugins: {
    legend: { position: 'top' as const },
    tooltip: {
      callbacks: {
        footer: (items: any[]) => {
          const total = items.reduce((s: number, i: any) => s + i.parsed.y, 0)
          return total > 0 ? `Total: ${total.toFixed(2)} kWh` : ''
        },
      },
    },
  },
}))
</script>

<template>
  <div class="p-6 space-y-6">
    <!-- Date range filter -->
    <div class="flex flex-wrap items-end gap-4">
      <div class="flex flex-col gap-1">
        <label class="text-xs font-medium text-gray-600">View</label>
        <div class="inline-flex rounded border border-gray-300 overflow-hidden text-sm">
          <button
            v-for="mode in (['daily', 'monthly'] as const)"
            :key="mode"
            :class="[
              'px-3 py-1.5 font-medium capitalize',
              viewMode === mode ? 'bg-blue-600 text-white' : 'bg-white text-gray-700 hover:bg-gray-50',
            ]"
            :disabled="loading"
            @click="setViewMode(mode)"
          >
            {{ mode }}
          </button>
        </div>
      </div>
      <div class="flex flex-col gap-1">
        <label class="text-xs font-medium text-gray-600">Start date</label>
        <input
          v-model="startDate"
          type="date"
          class="border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <div class="flex flex-col gap-1">
        <label class="text-xs font-medium text-gray-600">End date</label>
        <input
          v-model="endDate"
          type="date"
          class="border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
      </div>
      <button
        class="px-4 py-1.5 bg-blue-600 text-white text-sm font-medium rounded hover:bg-blue-700 disabled:opacity-50"
        :disabled="loading"
        @click="load"
      >
        {{ loading ? 'Loading…' : 'Apply' }}
      </button>
    </div>

    <!-- Error -->
    <div v-if="error" class="text-sm text-red-600 bg-red-50 border border-red-200 rounded px-4 py-2">
      {{ error }}
    </div>

    <!-- Summary stats -->
    <div v-if="points.length > 0" class="grid grid-cols-3 gap-4">
      <div class="bg-white border border-gray-200 rounded-lg px-4 py-3">
        <div class="text-xs text-gray-500 mb-1">Total usage</div>
        <div class="text-xl font-semibold text-gray-800">{{ totalKwh.toFixed(1) }} kWh</div>
      </div>
      <div class="bg-white border border-gray-200 rounded-lg px-4 py-3">
        <template v-if="viewMode === 'monthly'">
          <div class="text-xs text-gray-500 mb-1">Monthly average</div>
          <div class="text-xl font-semibold text-gray-800">{{ avgMonthlyKwh.toFixed(1) }} kWh</div>
        </template>
        <template v-else>
          <div class="text-xs text-gray-500 mb-1">Daily average</div>
          <div class="text-xl font-semibold text-gray-800">{{ avgDailyKwh.toFixed(1) }} kWh</div>
        </template>
      </div>
      <div class="bg-white border border-gray-200 rounded-lg px-4 py-3">
        <div class="text-xs text-gray-500 mb-1">Estimated days</div>
        <div class="text-xl font-semibold" :class="estimatedDays > 0 ? 'text-amber-600' : 'text-gray-800'">
          {{ estimatedDays }} / {{ points.length }}
        </div>
      </div>
    </div>

    <!-- Chart -->
    <div v-if="points.length > 0" class="bg-white border border-gray-200 rounded-lg p-4">
      <div class="relative" style="height: 360px">
        <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
          <svg class="animate-spin h-8 w-8 text-blue-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
          </svg>
        </div>
        <Bar :data="chartData" :options="chartOptions" style="height: 360px" />
      </div>
    </div>

    <!-- No data -->
    <div
      v-else-if="!loading && !error"
      class="text-center text-gray-400 py-16 bg-white border border-gray-200 rounded-lg"
    >
      No usage data found for the selected date range.
    </div>

    <!-- Data table -->
    <div v-if="points.length > 0" class="bg-white border border-gray-200 rounded-lg overflow-hidden">
      <table class="w-full text-sm">
        <thead class="bg-gray-50 border-b border-gray-200">
          <tr>
            <th class="text-left px-4 py-2 font-medium text-gray-600">{{ viewMode === 'monthly' ? 'Month' : 'Date' }}</th>
            <th class="text-right px-4 py-2 font-medium text-gray-600">Usage (kWh)</th>
            <th v-if="viewMode === 'monthly'" class="text-right px-4 py-2 font-medium text-gray-600">Daily avg (kWh)</th>
            <th v-if="viewMode === 'monthly'" class="text-right px-4 py-2 font-medium text-gray-600">Days</th>
            <th class="text-center px-4 py-2 font-medium text-gray-600">Reading</th>
          </tr>
        </thead>
        <tbody v-if="viewMode === 'monthly'" class="divide-y divide-gray-100">
          <tr v-for="r in rows" :key="r.label" class="hover:bg-gray-50">
            <td class="px-4 py-1.5 text-gray-700 font-mono text-xs">{{ r.label }}</td>
            <td class="px-4 py-1.5 text-right text-gray-800 font-medium">
              {{ (r.actualKwh + r.estimatedKwh).toFixed(2) }}
            </td>
            <td class="px-4 py-1.5 text-right text-gray-700">
              {{ ((r.actualKwh + r.estimatedKwh) / r.days).toFixed(2) }}
            </td>
            <td
              class="px-4 py-1.5 text-right"
              :class="r.days < daysInMonth(r.label) ? 'text-amber-600' : 'text-gray-700'"
              :title="r.days < daysInMonth(r.label) ? 'Partial month' : ''"
            >
              {{ r.days }} / {{ daysInMonth(r.label) }}
            </td>
            <td class="px-4 py-1.5 text-center">
              <span
                :class="[
                  'inline-block px-2 py-0.5 rounded-full text-xs font-medium',
                  r.estimatedDays === 0 ? 'bg-blue-100 text-blue-700' : 'bg-amber-100 text-amber-700',
                ]"
              >
                {{ r.estimatedDays === 0 ? 'Actual' : `${r.estimatedDays} est. day${r.estimatedDays === 1 ? '' : 's'}` }}
              </span>
            </td>
          </tr>
        </tbody>
        <tbody v-else class="divide-y divide-gray-100">
          <tr v-for="p in points" :key="p.date" class="hover:bg-gray-50">
            <td class="px-4 py-1.5 text-gray-700 font-mono text-xs">{{ p.date }}</td>
            <td class="px-4 py-1.5 text-right text-gray-800 font-medium">{{ p.kwh.toFixed(2) }}</td>
            <td class="px-4 py-1.5 text-center">
              <span
                :class="[
                  'inline-block px-2 py-0.5 rounded-full text-xs font-medium',
                  p.is_actual ? 'bg-blue-100 text-blue-700' : 'bg-amber-100 text-amber-700',
                ]"
              >
                {{ p.is_actual ? 'Actual' : 'Estimated' }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
