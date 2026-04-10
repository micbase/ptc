<script setup lang="ts">
import { ref, computed } from 'vue'
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS,
  LineElement,
  PointElement,
  LinearScale,
  CategoryScale,
  Tooltip,
  Legend,
  type ChartOptions,
  type ChartData,
} from 'chart.js'
import { fetchProjection } from '../api'
import type { StrategyResult, ProjectionRequest } from '../types'

ChartJS.register(LineElement, PointElement, LinearScale, CategoryScale, Tooltip, Legend)

// ── Form state ────────────────────────────────────────────────────────────────
const currentRateCents = ref<number | ''>('')
const currentBaseFee = ref<number | ''>('')
const etfAmount = ref<number | ''>(0)
const contractExpiration = ref('')

const loading = ref(false)
const error = ref('')
const strategies = ref<StrategyResult[]>([])

// ── Table sort state ──────────────────────────────────────────────────────────
const sortKey = ref<keyof StrategyResult>('net_savings')
const sortAsc = ref(false)

// ── Chart filter ──────────────────────────────────────────────────────────────
const selectedStrategyId = ref<string | null>(null)

// ── Strategy config ───────────────────────────────────────────────────────────
const STRATEGY_COLORS: Record<string, string> = {
  baseline: '#6b7280',
  switch_at_expiry_12m: '#3b82f6',
  switch_at_expiry_6m: '#8b5cf6',
  switch_at_expiry_3m: '#f59e0b',
  switch_now_12m: '#10b981',
  switch_now_3m: '#f97316',
  optimal_greedy: '#ef4444',
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function hexToRgba(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r},${g},${b},${alpha})`
}

function isSwitchMonth(strategy: StrategyResult, month: string): boolean {
  return strategy.switches.some((sw) => sw.effective_month === month)
}

function switchEtfForMonth(strategy: StrategyResult, month: string): number {
  const sw = strategy.switches.find((s) => s.effective_month === month)
  return sw?.etf_paid ?? 0
}

function isVariableMonth(strategy: StrategyResult, month: string): boolean {
  const mb = strategy.monthly_breakdown.find((m) => m.month === month)
  return mb?.active_plan_label.includes('Variable') ?? false
}

// ── Filtered strategies for charts ───────────────────────────────────────────
const visibleStrategies = computed(() => {
  if (!selectedStrategyId.value) return strategies.value
  return strategies.value.filter(
    (s) => s.strategy_id === selectedStrategyId.value || s.strategy_id === 'baseline',
  )
})

const monthLabels = computed(() =>
  strategies.value[0]?.monthly_breakdown.map((m) => m.month) ?? [],
)

// ── Monthly cost chart data ───────────────────────────────────────────────────
const monthlyCostData = computed<ChartData<'line'>>(() => {
  const labels = monthLabels.value
  const datasets = visibleStrategies.value.map((s) => {
    const color = STRATEGY_COLORS[s.strategy_id] ?? '#888'
    const monthCosts = s.monthly_breakdown.map((m) => m.monthly_cost)
    const switchMonths = new Set(s.switches.map((sw) => sw.effective_month))

    return {
      label: s.strategy_name,
      data: monthCosts,
      borderColor: color,
      backgroundColor: color,
      borderWidth: 2,
      tension: 0.1,
      pointRadius: labels.map((lbl) => (switchMonths.has(lbl) ? 7 : 2)),
      pointHoverRadius: 9,
      pointStyle: labels.map((lbl) => {
        const etf = switchEtfForMonth(s, lbl)
        if (switchMonths.has(lbl) && etf > 0) return 'star' as const
        if (switchMonths.has(lbl)) return 'circle' as const
        return 'circle' as const
      }),
      segment: {
        borderDash: (ctx: any) => (isVariableMonth(s, labels[ctx.p0DataIndex]) ? [6, 3] : []),
        borderColor: (ctx: any) => {
          const idx = ctx.p0DataIndex
          const isLowConf = idx >= 6
          return hexToRgba(color, isLowConf && isVariableMonth(s, labels[idx]) ? 0.45 : 1)
        },
      },
    }
  })
  return { labels, datasets }
})

const monthlyCostOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: {
    x: { ticks: { maxRotation: 45 } },
    y: { title: { display: true, text: 'Monthly Cost ($)' } },
  },
  plugins: {
    legend: { position: 'top' },
    tooltip: {
      callbacks: {
        afterLabel: (ctx) => {
          const s = visibleStrategies.value[ctx.datasetIndex]
          const lbl = monthLabels.value[ctx.dataIndex]
          const sw = s?.switches.find((sw) => sw.effective_month === lbl)
          if (!sw) return ''
          const parts: string[] = [`  ↳ Switch to ${sw.plan.rep_company} @ ${sw.plan.projected_rate_cents.toFixed(2)}¢/kWh`]
          if (sw.etf_paid > 0) parts.push(`  ⚠ ETF: $${sw.etf_paid.toFixed(2)}`)
          return parts.join('\n')
        },
      },
    },
  },
}))

// ── Cumulative cost chart data ────────────────────────────────────────────────
const cumulativeCostData = computed<ChartData<'line'>>(() => {
  const labels = monthLabels.value
  const datasets = visibleStrategies.value.map((s) => {
    const color = STRATEGY_COLORS[s.strategy_id] ?? '#888'
    const etfByMonth: Record<string, number> = {}
    s.switches.forEach((sw) => {
      if (sw.etf_paid > 0) etfByMonth[sw.effective_month] = (etfByMonth[sw.effective_month] ?? 0) + sw.etf_paid
    })

    let running = 0
    const cumData = s.monthly_breakdown.map((mb) => {
      running += mb.monthly_cost + (etfByMonth[mb.month] ?? 0)
      return Math.round(running * 100) / 100
    })

    return {
      label: `${s.strategy_name} ($${s.total_cost.toFixed(0)})`,
      data: cumData,
      borderColor: color,
      backgroundColor: color,
      borderWidth: 2,
      tension: 0.1,
      pointRadius: labels.map((lbl) => {
        const etf = switchEtfForMonth(s, lbl)
        return etf > 0 ? 7 : 2
      }),
      pointStyle: labels.map((lbl) => {
        const etf = switchEtfForMonth(s, lbl)
        return etf > 0 ? ('star' as const) : ('circle' as const)
      }),
      pointHoverRadius: 9,
    }
  })
  return { labels, datasets }
})

const cumulativeCostOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: {
    x: { ticks: { maxRotation: 45 } },
    y: { title: { display: true, text: 'Cumulative Cost ($)' } },
  },
  plugins: {
    legend: { position: 'top' },
    tooltip: {
      callbacks: {
        afterLabel: (ctx) => {
          const s = visibleStrategies.value[ctx.datasetIndex]
          const lbl = monthLabels.value[ctx.dataIndex]
          const etf = switchEtfForMonth(s, lbl)
          if (etf > 0) return `  ⚠ ETF paid: $${etf.toFixed(2)}`
          return ''
        },
      },
    },
  },
}))

// ── Table ─────────────────────────────────────────────────────────────────────
const sortedStrategies = computed(() => {
  const list = [...strategies.value]
  list.sort((a, b) => {
    const av = a[sortKey.value] as number
    const bv = b[sortKey.value] as number
    return sortAsc.value ? (av as any) - (bv as any) : (bv as any) - (av as any)
  })
  return list
})

function setSort(key: keyof StrategyResult) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = false
  }
}

const bestNetSavingsId = computed(() => {
  let best: StrategyResult | null = null
  for (const s of strategies.value) {
    if (s.strategy_id === 'baseline') continue
    if (!best || s.net_savings > best.net_savings) best = s
  }
  return best?.strategy_id ?? null
})

function onRowClick(id: string) {
  if (id === 'baseline') return
  selectedStrategyId.value = selectedStrategyId.value === id ? null : id
}

// ── Submit ────────────────────────────────────────────────────────────────────
async function onSubmit() {
  error.value = ''
  strategies.value = []
  selectedStrategyId.value = null

  const req: ProjectionRequest = {
    current_rate_cents: Number(currentRateCents.value),
    current_base_fee: Number(currentBaseFee.value),
    etf_amount: Number(etfAmount.value) || 0,
    contract_expiration: contractExpiration.value,
  }

  loading.value = true
  try {
    strategies.value = await fetchProjection(req)
  } catch (e: any) {
    error.value = e.message ?? 'Projection failed'
  } finally {
    loading.value = false
  }
}

const confidenceClass = (c: string) =>
  c === 'high' ? 'text-green-700' : c === 'medium' ? 'text-yellow-700' : 'text-red-600'

function sortIcon(key: keyof StrategyResult): string {
  if (sortKey.value !== key) return '↕'
  return sortAsc.value ? '↑' : '↓'
}
</script>

<template>
  <div class="w-full px-4 py-6">
    <h1 class="text-2xl font-bold text-gray-900 mb-6">Switch Planner — 12-Month Projection</h1>

    <!-- Input Form -->
    <div class="bg-white rounded-lg shadow p-5 mb-6">
      <h2 class="text-base font-semibold text-gray-700 mb-4">Your Current Plan</h2>
      <form @submit.prevent="onSubmit" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">
            Current Rate (¢/kWh)
          </label>
          <input
            v-model="currentRateCents"
            type="number"
            step="0.01"
            min="0"
            required
            placeholder="e.g. 10.5"
            class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">
            Monthly Base Fee ($)
          </label>
          <input
            v-model="currentBaseFee"
            type="number"
            step="0.01"
            min="0"
            required
            placeholder="e.g. 9.95"
            class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">
            Early Termination Fee ($)
          </label>
          <input
            v-model="etfAmount"
            type="number"
            step="0.01"
            min="0"
            placeholder="0 if none"
            class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-gray-600 mb-1">
            Contract Expiration
          </label>
          <input
            v-model="contractExpiration"
            type="date"
            required
            class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
          />
        </div>
        <div class="sm:col-span-2 lg:col-span-4 flex items-center gap-4">
          <button
            type="submit"
            :disabled="loading"
            class="px-5 py-2 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {{ loading ? 'Computing…' : 'Run Projection' }}
          </button>
          <div v-if="loading" class="flex items-center gap-2 text-sm text-gray-500">
            <svg class="animate-spin h-4 w-4 text-blue-500" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
            </svg>
            Calculating strategies…
          </div>
        </div>
      </form>
      <div v-if="error" class="mt-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
        {{ error }}
      </div>
    </div>

    <!-- Results -->
    <template v-if="strategies.length > 0">
      <!-- Filter notice -->
      <div v-if="selectedStrategyId" class="mb-3 flex items-center gap-2 text-sm text-blue-700 bg-blue-50 border border-blue-200 rounded px-3 py-2">
        <span>Charts filtered: showing <strong>{{ strategies.find(s => s.strategy_id === selectedStrategyId)?.strategy_name }}</strong> vs baseline.</span>
        <button class="ml-auto underline" @click="selectedStrategyId = null">Show all</button>
      </div>

      <!-- Panel 1: Monthly Cost Chart -->
      <div class="bg-white rounded-lg shadow p-4 mb-6">
        <h2 class="text-base font-semibold text-gray-700 mb-3">Monthly Cost by Strategy</h2>
        <div style="height: 380px">
          <Line :data="monthlyCostData" :options="monthlyCostOptions" style="height: 380px" />
        </div>
        <p class="mt-2 text-xs text-gray-400">
          Circles on lines = switch events. ★ = ETF paid at switch. Dashed segments = variable plan months. Months 7-12 on non-fixed projections shown at reduced opacity.
        </p>
      </div>

      <!-- Panel 2: Cumulative Cost Chart -->
      <div class="bg-white rounded-lg shadow p-4 mb-6">
        <h2 class="text-base font-semibold text-gray-700 mb-3">Cumulative Cost Over 12 Months</h2>
        <div style="height: 380px">
          <Line :data="cumulativeCostData" :options="cumulativeCostOptions" style="height: 380px" />
        </div>
        <p class="mt-2 text-xs text-gray-400">
          Includes ETF cost at the month it is paid. Legend labels show 12-month total cost. ★ = month ETF was paid.
        </p>
      </div>

      <!-- Panel 3: Strategy Comparison Table -->
      <div class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <div class="px-4 py-3 border-b border-gray-100 flex items-center gap-3">
          <h2 class="text-base font-semibold text-gray-700">Strategy Comparison</h2>
          <span class="text-xs text-gray-400">Click a row to filter charts to that strategy vs baseline</span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-gray-50 text-gray-600 text-xs uppercase">
                <th class="text-left px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('strategy_name')">
                  Strategy {{ sortIcon('strategy_name') }}
                </th>
                <th class="text-right px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('total_cost')">
                  12-mo Total {{ sortIcon('total_cost') }}
                </th>
                <th class="text-right px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('total_savings_vs_baseline')">
                  Savings vs Baseline {{ sortIcon('total_savings_vs_baseline') }}
                </th>
                <th class="text-right px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('etf_paid')">
                  ETF Paid {{ sortIcon('etf_paid') }}
                </th>
                <th class="text-right px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('net_savings')">
                  Net Savings {{ sortIcon('net_savings') }}
                </th>
                <th class="text-right px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('switch_count')">
                  # Switches {{ sortIcon('switch_count') }}
                </th>
                <th class="text-center px-4 py-3 font-medium cursor-pointer whitespace-nowrap" @click="setSort('confidence')">
                  Confidence {{ sortIcon('confidence') }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="s in sortedStrategies"
                :key="s.strategy_id"
                :class="[
                  'border-t border-gray-100 transition-colors',
                  s.strategy_id === bestNetSavingsId ? 'bg-green-50' : 'hover:bg-gray-50',
                  s.strategy_id !== 'baseline' ? 'cursor-pointer' : '',
                  s.strategy_id === selectedStrategyId ? 'ring-2 ring-inset ring-blue-400' : '',
                ]"
                @click="onRowClick(s.strategy_id)"
              >
                <td class="px-4 py-3">
                  <div class="flex items-center gap-2">
                    <span
                      class="inline-block w-3 h-3 rounded-full flex-shrink-0"
                      :style="{ background: STRATEGY_COLORS[s.strategy_id] }"
                    />
                    <span class="font-medium text-gray-900">{{ s.strategy_name }}</span>
                    <span
                      v-if="s.strategy_id === bestNetSavingsId"
                      class="ml-1 text-xs bg-green-600 text-white px-1.5 py-0.5 rounded"
                    >Best</span>
                  </div>
                </td>
                <td class="px-4 py-3 text-right tabular-nums">${{ s.total_cost.toFixed(2) }}</td>
                <td class="px-4 py-3 text-right tabular-nums">
                  <span :class="s.total_savings_vs_baseline > 0 ? 'text-green-700' : 'text-gray-500'">
                    {{ s.total_savings_vs_baseline > 0 ? '+' : '' }}${{ s.total_savings_vs_baseline.toFixed(2) }}
                  </span>
                </td>
                <td class="px-4 py-3 text-right tabular-nums">
                  <span :class="s.etf_paid > 0 ? 'text-red-600' : 'text-gray-400'">
                    {{ s.etf_paid > 0 ? `$${s.etf_paid.toFixed(2)}` : '—' }}
                  </span>
                </td>
                <td class="px-4 py-3 text-right tabular-nums font-semibold">
                  <span :class="s.net_savings > 0 ? 'text-green-700' : s.net_savings < 0 ? 'text-red-600' : 'text-gray-500'">
                    {{ s.net_savings > 0 ? '+' : '' }}${{ s.net_savings.toFixed(2) }}
                  </span>
                </td>
                <td class="px-4 py-3 text-right tabular-nums text-gray-600">{{ s.switch_count }}</td>
                <td class="px-4 py-3 text-center">
                  <span :class="['text-xs font-medium capitalize', confidenceClass(s.confidence)]">
                    {{ s.confidence }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Monthly Breakdown for selected strategy -->
      <div v-if="selectedStrategyId" class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <div class="px-4 py-3 border-b border-gray-100">
          <h2 class="text-base font-semibold text-gray-700">
            Monthly Breakdown — {{ strategies.find(s => s.strategy_id === selectedStrategyId)?.strategy_name }}
          </h2>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-gray-50 text-gray-600 text-xs uppercase">
                <th class="text-left px-3 py-2 font-medium">Period</th>
                <th class="text-left px-3 py-2 font-medium">Date Range</th>
                <th class="text-right px-3 py-2 font-medium">Usage (kWh)</th>
                <th class="text-left px-3 py-2 font-medium">Plan</th>
                <th class="text-right px-3 py-2 font-medium">Rate (¢)</th>
                <th class="text-right px-3 py-2 font-medium">Base Fee</th>
                <th class="text-right px-3 py-2 font-medium">Cost</th>
                <th class="text-center px-3 py-2 font-medium">Confidence</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="mb in strategies.find(s => s.strategy_id === selectedStrategyId)?.monthly_breakdown"
                :key="mb.month"
                class="border-t border-gray-100"
              >
                <td class="px-3 py-2 font-mono text-gray-700">{{ mb.month }}</td>
                <td class="px-3 py-2 text-xs text-gray-500 whitespace-nowrap">{{ mb.period_start }} – {{ mb.period_end }}</td>
                <td class="px-3 py-2 text-right tabular-nums text-gray-700">
                  {{ mb.usage_kwh.toFixed(0) }}
                  <span v-if="mb.usage_is_estimated" class="text-gray-400 text-xs">~</span>
                </td>
                <td class="px-3 py-2 text-gray-600 max-w-xs truncate">{{ mb.active_plan_label }}</td>
                <td class="px-3 py-2 text-right tabular-nums text-gray-700">{{ mb.rate_cents.toFixed(2) }}</td>
                <td class="px-3 py-2 text-right tabular-nums text-gray-700">${{ mb.base_fee.toFixed(2) }}</td>
                <td class="px-3 py-2 text-right tabular-nums font-medium text-gray-900">${{ mb.monthly_cost.toFixed(2) }}</td>
                <td class="px-3 py-2 text-center">
                  <span :class="['text-xs font-medium capitalize', confidenceClass(mb.confidence)]">{{ mb.confidence }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>
  </div>
</template>
