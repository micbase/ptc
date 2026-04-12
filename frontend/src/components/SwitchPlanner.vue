<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
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
import { fetchProjection, fetchLatestSwitchEvent } from '../api'
import type { StrategyResult, PeriodBreakdown, ProjectionRequest, Plan, SwitchRecord } from '../types'
import EnrollConfirmModal from './EnrollConfirmModal.vue'

ChartJS.register(LineElement, PointElement, LinearScale, CategoryScale, Tooltip, Legend)

// ── Form state ────────────────────────────────────────────────────────────────
const etfText = ref('')
const contractExpiration = ref('')
const loadedFrom = ref<SwitchRecord | null>(null)

const loading = ref(false)
const error = ref('')
const strategies = ref<StrategyResult[]>([])

// ── ETF text parser ───────────────────────────────────────────────────────────
function parseEtfText(text: string): { etf_amount: number; etf_per_month_amount: number } {
  const t = text.trim()
  if (!t) return { etf_amount: 0, etf_per_month_amount: 0 }
  const perMonth = t.match(
    /^(\d+(?:\.\d+)?)\s*(?:\/\s*(?:remaining\s+months?|months?\s+remaining)|per\s+month(?:\s+remaining|\s+left\s+in\s+term)?)/i,
  )
  if (perMonth) return { etf_amount: 0, etf_per_month_amount: parseFloat(perMonth[1]) }
  const fixed = t.match(/^(\d+(?:\.\d+)?)/)
  if (fixed) return { etf_amount: parseFloat(fixed[1]), etf_per_month_amount: 0 }
  return { etf_amount: 0, etf_per_month_amount: 0 }
}

const parsedEtf = computed(() => parseEtfText(etfText.value))

const etfHint = computed(() => {
  const { etf_amount, etf_per_month_amount } = parsedEtf.value
  if (etf_per_month_amount > 0) return `$${etf_per_month_amount.toFixed(2)} × months remaining`
  if (etf_amount > 0) return `$${etf_amount.toFixed(2)} one-time fee`
  return 'No ETF'
})

// ── Override form visibility ──────────────────────────────────────────────────
const showOverride = ref(false)

// ── Auto-load from latest switch event ───────────────────────────────────────
onMounted(async () => {
  try {
    const latest = await fetchLatestSwitchEvent()
    if (latest) {
      loadedFrom.value = latest
      contractExpiration.value = latest.contract_expiration_date
      etfText.value = latest.cancel_fee || ''
      await onSubmit()
    }
  } catch {
    // silently ignore — user can fill manually
  }
})

// ── Table sort state ──────────────────────────────────────────────────────────
const sortKey = ref<keyof StrategyResult>('net_savings')
const sortAsc = ref(false)

// ── Selected strategy (for breakdown + chart filter) ─────────────────────────
const selectedStrategyId = ref<string | null>(null)

// ── Chart tab ─────────────────────────────────────────────────────────────────
const activeChartTab = ref<'period' | 'cumulative'>('period')

// ── Strategy config ───────────────────────────────────────────────────────────
const STRATEGY_COLORS: Record<string, string> = {
  baseline: '#6b7280',
  switch_at_expiry_12m: '#3b82f6',
  switch_at_expiry_6m: '#8b5cf6',
  switch_at_expiry_3m: '#f59e0b',
  switch_now_12m: '#10b981',
  switch_now_3m: '#f97316',
  switch_now_6m: '#06b6d4',
  switch_at_expiry_3m_or_4m: '#ec4899',
  optimal_greedy: '#ef4444',
}

// ── Helpers ───────────────────────────────────────────────────────────────────
function hexToRgba(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r},${g},${b},${alpha})`
}

function isSwitchPeriod(strategy: StrategyResult, period: string): boolean {
  return strategy.switches.some((sw) => sw.effective_period === period)
}

function switchEtfForPeriod(strategy: StrategyResult, period: string): number {
  const sw = strategy.switches.find((s) => s.effective_period === period)
  return sw?.etf_paid ?? 0
}

function isProjectedPeriod(strategy: StrategyResult, period: string): boolean {
  const pb = strategy.period_breakdown.find((m) => m.period === period)
  return pb?.is_projected ?? false
}

// ── Chart strategies: default = baseline + top 3 winners; selected = vs baseline ──
const chartStrategies = computed(() => {
  if (selectedStrategyId.value) {
    return strategies.value.filter(
      (s) => s.strategy_id === selectedStrategyId.value || s.strategy_id === 'baseline',
    )
  }
  const baseline = strategies.value.find((s) => s.strategy_id === 'baseline')
  const top3 = [...strategies.value]
    .filter((s) => s.strategy_id !== 'baseline')
    .sort((a, b) => b.net_savings - a.net_savings)
    .slice(0, 3)
  return [baseline, ...top3].filter(Boolean) as StrategyResult[]
})

const periodLabels = computed(() =>
  strategies.value[0]?.period_breakdown.map((m) => m.period) ?? [],
)

// ── Period cost chart data ────────────────────────────────────────────────────
const periodCostData = computed<ChartData<'line'>>(() => {
  const labels = periodLabels.value
  const datasets = chartStrategies.value.map((s) => {
    const color = STRATEGY_COLORS[s.strategy_id] ?? '#888'
    const periodCosts = s.period_breakdown.map((m) => m.period_cost)
    const switchPeriods = new Set(s.switches.map((sw) => sw.effective_period))

    return {
      label: s.strategy_name,
      data: periodCosts,
      borderColor: color,
      backgroundColor: color,
      borderWidth: 2,
      tension: 0.1,
      pointRadius: labels.map((lbl) => (switchPeriods.has(lbl) ? 7 : 2)),
      pointHoverRadius: 9,
      pointStyle: labels.map((lbl) => {
        const etf = switchEtfForPeriod(s, lbl)
        if (switchPeriods.has(lbl) && etf > 0) return 'star' as const
        if (switchPeriods.has(lbl)) return 'circle' as const
        return 'circle' as const
      }),
      segment: {
        borderDash: (ctx: any) => (isProjectedPeriod(s, labels[ctx.p0DataIndex]) ? [6, 3] : []),
        borderColor: (ctx: any) =>
          hexToRgba(color, isProjectedPeriod(s, labels[ctx.p0DataIndex]) ? 0.5 : 1),
      },
    }
  })
  return { labels, datasets }
})

const periodCostOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: {
    x: { ticks: { maxRotation: 45 } },
    y: { title: { display: true, text: 'Period Cost ($)' } },
  },
  plugins: {
    legend: { position: 'top' },
    tooltip: {
      callbacks: {
        afterLabel: (ctx) => {
          const s = chartStrategies.value[ctx.datasetIndex]
          const lbl = periodLabels.value[ctx.dataIndex]
          const sw = s?.switches.find((sw) => sw.effective_period === lbl)
          if (!sw) return ''
          const parts: string[] = [`  ↳ Switch to ${sw.plan.rep_company} @ ${sw.plan.per_kwh_rate.toFixed(2)}¢/kWh`]
          if (sw.etf_paid > 0) parts.push(`  ⚠ ETF: $${sw.etf_paid.toFixed(2)}`)
          return parts.join('\n')
        },
      },
    },
  },
}))

// ── Cumulative cost chart data ────────────────────────────────────────────────
const cumulativeCostData = computed<ChartData<'line'>>(() => {
  const labels = periodLabels.value
  const datasets = chartStrategies.value.map((s) => {
    const color = STRATEGY_COLORS[s.strategy_id] ?? '#888'
    const etfByPeriod: Record<string, number> = {}
    s.switches.forEach((sw) => {
      if (sw.etf_paid > 0) etfByPeriod[sw.effective_period] = (etfByPeriod[sw.effective_period] ?? 0) + sw.etf_paid
    })

    let running = 0
    const cumData = s.period_breakdown.map((pb) => {
      running += pb.period_cost + (etfByPeriod[pb.period] ?? 0)
      return Math.round(running * 100) / 100
    })

    return {
      label: `${s.strategy_name} ($${(s.total_cost + s.etf_paid).toFixed(0)})`,
      data: cumData,
      borderColor: color,
      backgroundColor: color,
      borderWidth: 2,
      tension: 0.1,
      pointRadius: labels.map((lbl) => {
        const etf = switchEtfForPeriod(s, lbl)
        return etf > 0 ? 7 : 2
      }),
      pointStyle: labels.map((lbl) => {
        const etf = switchEtfForPeriod(s, lbl)
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
          const s = chartStrategies.value[ctx.datasetIndex]
          const lbl = periodLabels.value[ctx.dataIndex]
          const etf = switchEtfForPeriod(s, lbl)
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
    const av = a[sortKey.value]
    const bv = b[sortKey.value]
    if (typeof av === 'string' && typeof bv === 'string') {
      return sortAsc.value ? av.localeCompare(bv) : bv.localeCompare(av)
    }
    return sortAsc.value ? (av as number) - (bv as number) : (bv as number) - (av as number)
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
  selectedStrategyId.value = selectedStrategyId.value === id ? null : id
}

// ── Submit ────────────────────────────────────────────────────────────────────
async function onSubmit() {
  error.value = ''
  strategies.value = []
  selectedStrategyId.value = null

  const { etf_amount, etf_per_month_amount } = parsedEtf.value
  const req: ProjectionRequest = {
    etf_amount,
    etf_per_month_amount,
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

// ── Enroll confirmation modal ─────────────────────────────────────────────────
const enrollModal = ref<{
  show: boolean
  plan: Plan | null
  switchDate: string
  expirationDate: string
}>({ show: false, plan: null, switchDate: '', expirationDate: '' })

function openEnrollModal(plan: Plan, periodStart: string) {
  let expDate = ''
  if (periodStart && plan.term_value > 1) {
    const d = new Date(periodStart)
    d.setMonth(d.getMonth() + plan.term_value)
    expDate = d.toISOString().slice(0, 10)
  }
  enrollModal.value = {
    show: true,
    plan,
    switchDate: periodStart || new Date().toISOString().slice(0, 10),
    expirationDate: expDate,
  }
}
</script>

<template>
  <div class="w-full px-4 py-6">

    <!-- Auto-loaded banner + expandable override form (same card) -->
    <div class="bg-white rounded-lg shadow mb-6">
      <!-- Banner row -->
      <div class="px-4 py-3 flex items-center gap-3">
        <div class="flex-1 min-w-0">
          <template v-if="loadedFrom">
            <span class="text-xs font-semibold text-blue-700 mr-1.5">Current Plan:</span>
            <span class="text-xs text-gray-700">{{ loadedFrom.rep_company }} — {{ loadedFrom.product }} (switched {{ loadedFrom.switch_date }}, expires {{ loadedFrom.contract_expiration_date }}, ETF: {{ etfHint }})</span>
          </template>
          <template v-else>
            <span class="text-xs text-gray-500">No switch history found — enter plan details manually.</span>
          </template>
        </div>
        <div class="flex items-center gap-3 shrink-0">
          <div v-if="loading" class="flex items-center gap-1.5 text-xs text-gray-500">
            <svg class="animate-spin h-3.5 w-3.5 text-blue-500" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
            </svg>
            Calculating…
          </div>
          <button
            @click="showOverride = !showOverride"
            class="px-3 py-1.5 text-xs font-medium rounded border border-gray-300 text-gray-600 hover:bg-gray-50"
          >
            {{ showOverride ? 'Hide' : 'Override' }}
          </button>
        </div>
      </div>

      <!-- Override form (expands within the same card) -->
      <div v-if="showOverride" class="border-t border-gray-100 px-4 py-4">
        <form @submit.prevent="onSubmit" class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Early Termination Fee</label>
            <input
              v-model="etfText"
              type="text"
              placeholder="e.g. 0, 20, 20/remaining month"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p class="mt-0.5 text-xs text-gray-400">{{ etfHint }}</p>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Contract Expiration</label>
            <input
              v-model="contractExpiration"
              type="date"
              required
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div class="sm:col-span-2 lg:col-span-4">
            <button
              type="submit"
              :disabled="loading"
              class="px-5 py-2 text-sm font-medium rounded bg-blue-600 text-white hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {{ loading ? 'Computing…' : 'Run Projection' }}
            </button>
          </div>
        </form>
        <div v-if="error" class="mt-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
          {{ error }}
        </div>
      </div>
    </div>

    <!-- Error when override form is hidden -->
    <div v-if="error && !showOverride" class="mb-6 text-sm text-red-700 bg-red-50 border border-red-200 rounded px-3 py-2">
      {{ error }}
    </div>

    <!-- Results -->
    <template v-if="strategies.length > 0">

      <!-- Strategy Comparison Table (with inline breakdown) -->
      <div class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <div class="px-4 py-3 border-b border-gray-100 flex items-center gap-3">
          <h2 class="text-base font-semibold text-gray-700">Strategy Comparison</h2>
          <span class="text-xs text-gray-400">Click a row to expand period breakdown</span>
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
              <template v-for="s in sortedStrategies" :key="s.strategy_id">
                <!-- Strategy row -->
                <tr
                  :class="[
                    'border-t border-gray-100 transition-colors',
                    s.strategy_id === bestNetSavingsId ? 'bg-green-50' : '',
                    s.strategy_id === selectedStrategyId ? 'bg-blue-50' : (s.strategy_id !== bestNetSavingsId ? 'hover:bg-gray-50' : 'hover:bg-green-100'),
                    'cursor-pointer',
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
                      <span class="ml-auto text-gray-400 text-xs">
                        {{ s.strategy_id === selectedStrategyId ? '▲' : '▼' }}
                      </span>
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

                <!-- Inline period breakdown (expands below selected row) -->
                <tr v-if="s.strategy_id === selectedStrategyId" :key="s.strategy_id + '-breakdown'">
                  <td colspan="7" class="p-0 bg-blue-50 border-t border-blue-200">
                    <div class="overflow-x-auto">
                      <table class="w-full text-xs">
                        <thead>
                          <tr class="text-gray-500 uppercase border-b border-blue-100">
                            <th class="text-left px-3 py-2 font-medium">Period</th>
                            <th class="text-left px-3 py-2 font-medium">Date Range</th>
                            <th class="text-right px-3 py-2 font-medium">Usage (kWh)</th>
                            <th class="text-left px-3 py-2 font-medium">Plan</th>
                            <th class="text-right px-3 py-2 font-medium">¢/kWh@1000</th>
                            <th class="text-right px-3 py-2 font-medium">Rate (¢)</th>
                            <th class="text-right px-3 py-2 font-medium">Base Fee</th>
                            <th class="text-right px-3 py-2 font-medium">Cost</th>
                            <th class="text-center px-3 py-2 font-medium">Confidence</th>
                          </tr>
                        </thead>
                        <tbody>
                          <tr
                            v-for="pb in s.period_breakdown"
                            :key="pb.period"
                            class="border-t border-blue-100"
                          >
                            <td class="px-3 py-2 font-mono text-gray-700">{{ pb.period }}</td>
                            <td class="px-3 py-2 text-gray-500 whitespace-nowrap">{{ pb.period_start }} – {{ pb.period_end }}</td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">
                              {{ pb.usage_kwh.toFixed(0) }}
                              <span v-if="pb.usage_is_estimated" class="text-gray-400">~</span>
                            </td>
                            <td class="px-3 py-2 text-gray-600 max-w-xs">
                              {{ pb.active_plan.rep_company }} — {{ pb.active_plan.product }}
                              <span class="text-gray-400">({{ pb.active_plan.term_value === 1 ? 'Variable' : `${pb.active_plan.term_value}m Fixed` }})</span>
                              <button
                                v-if="pb.active_plan.enroll_url && !pb.is_projected"
                                @click.stop="openEnrollModal(pb.active_plan, pb.period_start)"
                                class="ml-2 text-blue-600 hover:underline"
                              >Enroll</button>
                            </td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">{{ pb.active_plan.kwh1000_cents.toFixed(2) }}</td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">
                              {{ pb.rate_cents.toFixed(2) }}<span v-if="pb.is_projected" class="ml-0.5 text-gray-400" title="Projected from historical data">~</span>
                            </td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">${{ pb.base_fee.toFixed(2) }}</td>
                            <td class="px-3 py-2 text-right tabular-nums font-medium text-gray-900">${{ pb.period_cost.toFixed(2) }}</td>
                            <td class="px-3 py-2 text-center">
                              <span :class="['font-medium capitalize', confidenceClass(pb.confidence)]">{{ pb.confidence }}</span>
                            </td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
      </div>

      <!-- Charts (tabbed) -->
      <div class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <!-- Tab bar -->
        <div class="border-b border-gray-200 flex items-center gap-1 px-4 pt-3">
          <button
            v-for="tab in [{ id: 'period', label: 'Period Cost' }, { id: 'cumulative', label: 'Cumulative Cost' }]"
            :key="tab.id"
            @click="activeChartTab = tab.id as 'period' | 'cumulative'"
            :class="[
              'px-4 py-2 text-sm font-medium rounded-t border-b-2 transition-colors',
              activeChartTab === tab.id
                ? 'border-blue-500 text-blue-600 bg-blue-50'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:bg-gray-50',
            ]"
          >
            {{ tab.label }}
          </button>
          <span class="ml-auto text-xs text-gray-400 pb-2">
            <template v-if="selectedStrategyId">
              Showing {{ strategies.find(s => s.strategy_id === selectedStrategyId)?.strategy_name }} vs baseline
              <button class="ml-2 underline text-blue-600" @click="selectedStrategyId = null">Reset</button>
            </template>
            <template v-else>Baseline + top 3 — click a strategy row to compare</template>
          </span>
        </div>

        <!-- Period Cost Chart -->
        <div v-show="activeChartTab === 'period'" class="p-4">
          <div style="height: 360px">
            <Line :data="periodCostData" :options="periodCostOptions" style="height: 360px" />
          </div>
          <p class="mt-2 text-xs text-gray-400">
            Circles = switch events. ★ = ETF paid. Solid = live rates (can enroll now). Dashed + faded = projected.
          </p>
        </div>

        <!-- Cumulative Cost Chart -->
        <div v-show="activeChartTab === 'cumulative'" class="p-4">
          <div style="height: 360px">
            <Line :data="cumulativeCostData" :options="cumulativeCostOptions" style="height: 360px" />
          </div>
          <p class="mt-2 text-xs text-gray-400">
            Includes ETF at the month it is paid. Legend labels show 12-month total cost. ★ = month ETF was paid.
          </p>
        </div>
      </div>

    </template>

    <!-- Enroll Confirmation Modal -->
    <EnrollConfirmModal
      v-if="enrollModal.plan"
      :show="enrollModal.show"
      :electricity-rate-id="enrollModal.plan.electricity_rate_id"
      :rep-company="enrollModal.plan.rep_company"
      :product="enrollModal.plan.product"
      :term-value="enrollModal.plan.term_value"
      :kwh1000-cents="enrollModal.plan.kwh1000_cents"
      :enroll-url="enrollModal.plan.enroll_url"
      :suggested-switch-date="enrollModal.switchDate"
      :suggested-expiration-date="enrollModal.expirationDate"
      @close="enrollModal.show = false"
      @recorded="enrollModal.show = false"
    />
  </div>
</template>
