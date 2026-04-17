<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Line, Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  LineElement,
  LineController,
  PointElement,
  LinearScale,
  CategoryScale,
  Tooltip,
  Legend,
  BarElement,
  BarController,
  type ChartOptions,
  type ChartData,
} from 'chart.js'
import { fetchProjection, fetchLatestSwitchEvent } from '../api'
import type { StrategySweep, SweepEntry, PeriodBreakdown, ProjectionRequest, Plan, SwitchRecord, PlanKind } from '../types'
import EnrollConfirmModal from './EnrollConfirmModal.vue'

ChartJS.register(LineElement, LineController, PointElement, LinearScale, CategoryScale, Tooltip, Legend, BarElement, BarController)

// ── Form state ────────────────────────────────────────────────────────────────
const etfText = ref('')
const contractExpiration = ref('')
const currentPlanCents = ref(0)
const currentPlanBaseFee = ref(0)
const loadedFrom = ref<SwitchRecord | null>(null)

const loading = ref(false)
const error = ref('')
const sweeps = ref<StrategySweep[]>([])

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
      currentPlanCents.value = latest.per_kwh_rate
      currentPlanBaseFee.value = latest.base_fee
      await onSubmit()
    }
  } catch {
    // silently ignore — user can fill manually
  }
})

// ── Strategy colors ───────────────────────────────────────────────────────────
const SWEEP_COLORS: Record<string, string> = {
  variable: '#6b7280',       // gray — baseline
  rolling_3m: '#f59e0b',     // amber
  rolling_6m: '#8b5cf6',     // purple
  fixed_12m: '#3b82f6',      // blue
  optimal_greedy: '#ef4444', // red
}

function hexToRgba(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r},${g},${b},${alpha})`
}

// ── Selection state ───────────────────────────────────────────────────────────
// null = show all strategies at best entry; otherwise a specific (strategyId, offset) pair
const selectedStrategyId = ref<string | null>(null)
const selectedOffset = ref<number | null>(null)

// ── Cost view mode ────────────────────────────────────────────────────────────
// false = post-switch only (apples-to-apples); true = full lifecycle (pre + ETF + post + post-expiry)
const totalCostMode = ref(false)
const postExpiryRateCents = ref(20) // ¢/kWh after the new plan's 12m term ends

function computePostExpiryCost(entry: SweepEntry): number {
  if (entry.period_breakdown.length === 0) return 0
  const avgMonthlyKwh = entry.period_breakdown.reduce((s, p) => s + p.usage_kwh, 0) / entry.period_breakdown.length
  return avgMonthlyKwh * 12 * (postExpiryRateCents.value / 100)
}

// Returns the best entry index for a sweep based on the current cost mode.
// In post-switch only mode: backend-provided best_entry_index_post_switch (lowest post_switch_cost).
// In total cost mode: backend-provided best_entry_index (lowest total_cost).
function effectiveBestIndex(sweep: StrategySweep): number {
  return totalCostMode.value ? sweep.best_entry_index : sweep.best_entry_index_post_switch
}

// X-axis labels
const offsetLabels = computed(() => {
  if (sweeps.value.length === 0) return []
  return sweeps.value[0].entries.map((e, i) =>
    i === 0 ? 'Now' : `+${e.weeks_from_today}w`
  )
})

// ── Best overall ─────────────────────────────────────────────────────────────
const bestOverall = computed<{ sweep: StrategySweep; entry: SweepEntry; entryIndex: number } | null>(() => {
  if (sweeps.value.length === 0) return null
  let best: { sweep: StrategySweep; entry: SweepEntry; entryIndex: number } | null = null
  for (const sweep of sweeps.value) {
    if (sweep.strategy_id === 'variable') continue
    const idx = effectiveBestIndex(sweep)
    const entry = sweep.entries[idx]
    if (!best || entry.savings_vs_baseline > best.entry.savings_vs_baseline) {
      best = { sweep, entry, entryIndex: idx }
    }
  }
  return best
})

const recommendation = computed(() => {
  const b = bestOverall.value
  if (!b) return ''
  const months = b.entryIndex
  const when = months === 0 ? 'immediately' : months === 1 ? 'in 1 month' : `in ${months} months`
  const savings = b.entry.savings_vs_baseline
  const etf = b.entry.etf_applied
  let msg = `Best: ${b.sweep.strategy_name} ${when}`
  if (savings > 0) msg += `, saves $${savings.toFixed(0)} vs variable baseline`
  if (etf > 0) msg += ` (includes $${etf.toFixed(0)} ETF)`
  else if (months > 0) msg += ` — no ETF`
  const as = b.entry.action_date_start
  const ae = b.entry.action_date_end
  if (as && ae) msg += ` · enroll ${as} – ${ae}`
  return msg
})

// ── Sweep chart ───────────────────────────────────────────────────────────────
const sweepChartData = computed<ChartData<'line'>>(() => {
  const labels = offsetLabels.value
  const datasets = sweeps.value.map((sweep) => {
    const color = SWEEP_COLORS[sweep.strategy_id] ?? '#888'
    const data = sweep.entries.map((e) => totalCostMode.value ? e.total_cost : e.post_switch_cost)
    const bestIdx = effectiveBestIndex(sweep)
    const pointRadius = sweep.entries.map((_, i) => i === bestIdx ? 8 : 3)
    const pointStyle = sweep.entries.map((_, i) =>
      i === bestIdx ? ('rectRot' as const) : ('circle' as const)
    )
    const isSelected = selectedStrategyId.value !== null && selectedStrategyId.value === sweep.strategy_id
    const isDeemphasized = selectedStrategyId.value !== null && !isSelected
    return {
      label: sweep.strategy_name,
      data,
      borderColor: isDeemphasized ? hexToRgba(color, 0.25) : color,
      backgroundColor: isDeemphasized ? hexToRgba(color, 0.25) : color,
      borderWidth: isSelected ? 3 : 2,
      tension: 0.15,
      pointRadius,
      pointHoverRadius: 10,
      pointStyle,
    }
  })
  return { labels, datasets }
})

const sweepChartOptions = computed<ChartOptions<'line'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: { mode: 'index', intersect: false },
  scales: {
    x: {
      title: { display: true, text: 'Switch entry (months from today)' },
      ticks: { maxRotation: 0 },
    },
    y: { title: { display: true, text: totalCostMode.value ? 'Total cost ($)' : 'Post-switch cost ($)' } },
  },
  plugins: {
    legend: { position: 'top' },
    tooltip: {
      callbacks: {
        title: (items) => {
          const idx = items[0]?.dataIndex ?? 0
          if (!sweeps.value[0]) return offsetLabels.value[idx] ?? ''
          const ws = sweeps.value[0].entries[idx]?.window_start ?? ''
          return `${offsetLabels.value[idx]} — enter ${ws}`
        },
        label: (ctx) => {
          const sweep = sweeps.value[ctx.datasetIndex]
          if (!sweep) return ''
          const entry = sweep.entries[ctx.dataIndex]
          if (!entry) return `${sweep.strategy_name}: $${(ctx.raw as number).toFixed(2)}`
          const isBest = ctx.dataIndex === effectiveBestIndex(sweep)
          const displayCost = totalCostMode.value ? entry.total_cost : entry.post_switch_cost
          const parts = [`${sweep.strategy_name}: $${displayCost.toFixed(2)}${isBest ? ' ★ best' : ''}`]
          if (totalCostMode.value) {
            if (entry.pre_switch_cost > 0) parts.push(`  pre-switch: $${entry.pre_switch_cost.toFixed(2)}`)
            if (entry.etf_applied > 0) parts.push(`  ETF: $${entry.etf_applied.toFixed(2)}`)
            parts.push(`  post-switch: $${entry.post_switch_cost.toFixed(2)}`)
          }
          if (entry.savings_vs_baseline > 0) parts.push(`  saves: $${entry.savings_vs_baseline.toFixed(2)}`)
          return parts
        },
      },
    },
  },
  onClick: (_event: any, elements: any[]) => {
    if (elements.length > 0) {
      const el = elements[0]
      const strategyId = sweeps.value[el.datasetIndex]?.strategy_id ?? null
      const offset = el.index
      if (selectedStrategyId.value === strategyId && selectedOffset.value === offset) {
        // Deselect on second click
        selectedStrategyId.value = null
        selectedOffset.value = null
      } else {
        selectedStrategyId.value = strategyId
        selectedOffset.value = offset
      }
    }
  },
}))

// ── Selected entry (for breakdown) ───────────────────────────────────────────
const selectedEntry = computed<SweepEntry | null>(() => {
  if (selectedStrategyId.value === null) return null
  const sweep = sweeps.value.find((s) => s.strategy_id === selectedStrategyId.value)
  if (!sweep) return null
  const idx = selectedOffset.value ?? effectiveBestIndex(sweep)
  return sweep.entries[idx] ?? null
})

const selectedSweep = computed<StrategySweep | null>(() =>
  sweeps.value.find((s) => s.strategy_id === selectedStrategyId.value) ?? null
)

const selectedEntryPostExpiryCost = computed(() =>
  selectedEntry.value ? computePostExpiryCost(selectedEntry.value) : 0
)


// ── Breakdown stacked bar chart ───────────────────────────────────────────────
const breakdownChartData = computed(() => {
  const sweep = selectedSweep.value
  if (!sweep) return { labels: [], datasets: [] }

  const activeBestIdx = effectiveBestIndex(sweep)
  const activeIdx = selectedOffset.value ?? activeBestIdx

  const labels = sweep.entries.map((e, i) => (i === 0 ? 'Now' : `+${e.weeks_from_today}w`))

  // Color constants (fixed across all strategies)
  const POST_SWITCH_COLOR = '#10b981' // emerald
  const PRE_SWITCH_COLOR = '#9ca3af'  // gray
  const ETF_COLOR = '#ef4444'          // red
  const POST_EXPIRY_COLOR = '#f97316' // orange

  const hi = (base: string, i: number) => (i === activeIdx ? base : hexToRgba(base, 0.35))

  const datasets: any[] = []

  if (totalCostMode.value) {
    datasets.push({
      type: 'bar' as const,
      label: 'Pre-switch',
      data: sweep.entries.map((e) => e.pre_switch_cost),
      backgroundColor: sweep.entries.map((_, i) => hi(PRE_SWITCH_COLOR, i)),
      stack: 'cost',
    })
    datasets.push({
      type: 'bar' as const,
      label: 'ETF',
      data: sweep.entries.map((e) => e.etf_applied),
      backgroundColor: sweep.entries.map((_, i) => hi(ETF_COLOR, i)),
      stack: 'cost',
    })
  }

  datasets.push({
    type: 'bar' as const,
    label: 'Post-switch (12m)',
    data: sweep.entries.map((e) => e.post_switch_cost),
    backgroundColor: sweep.entries.map((_, i) => hi(POST_SWITCH_COLOR, i)),
    stack: 'cost',
  })

  if (totalCostMode.value) {
    datasets.push({
      type: 'bar' as const,
      label: `Post-expiry (${postExpiryRateCents.value}¢/kWh est.)`,
      data: sweep.entries.map((e) => computePostExpiryCost(e)),
      backgroundColor: sweep.entries.map((_, i) => hi(POST_EXPIRY_COLOR, i)),
      stack: 'cost',
    })
  }

  return { labels, datasets }
})

const breakdownChartOptions = computed(() => {
  const sweep = selectedSweep.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    scales: {
      x: {
        stacked: true,
        ticks: { maxRotation: 0 },
      },
      y: {
        stacked: true,
        title: { display: true, text: 'Total cost ($)' },
        ticks: {
          callback: (v: number | string) => `$${Number(v).toFixed(0)}`,
        },
      },
    },
    plugins: {
      legend: { position: 'top' as const },
      tooltip: {
        callbacks: {
          title: (items: any[]) => {
            const idx = items[0]?.dataIndex ?? 0
            const entry = sweep?.entries[idx]
            const label = idx === 0 ? 'Now' : `+${entry?.weeks_from_today}w`
            return entry ? `${label} — enter ${entry.window_start}` : label
          },
          label: (ctx: any) => {
            const val = ctx.raw as number
            if (!val) return null
            return `${ctx.dataset.label}: $${val.toFixed(2)}`
          },
        },
      },
    },
    onClick: (_event: any, elements: any[]) => {
      if (elements.length > 0) {
        const idx = elements[0].index
        selectedOffset.value = idx
      }
    },
  }
})

// ── Strategy best-entry summary table ────────────────────────────────────────
function onRowClick(strategyId: string) {
  if (selectedStrategyId.value === strategyId && selectedOffset.value === null) {
    selectedStrategyId.value = null
    selectedOffset.value = null
  } else {
    selectedStrategyId.value = strategyId
    selectedOffset.value = null // default to best entry
  }
}

// ── Submit ────────────────────────────────────────────────────────────────────
async function onSubmit() {
  error.value = ''
  sweeps.value = []
  selectedStrategyId.value = null
  selectedOffset.value = null

  const { etf_amount, etf_per_month_amount } = parsedEtf.value
  const req: ProjectionRequest = {
    etf_amount,
    etf_per_month_amount,
    contract_expiration: contractExpiration.value,
    current_plan_cents: currentPlanCents.value,
    current_plan_base_fee: currentPlanBaseFee.value,
  }

  loading.value = true
  try {
    sweeps.value = await fetchProjection(req)
  } catch (e: any) {
    error.value = e.message ?? 'Projection failed'
  } finally {
    loading.value = false
  }
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

    <!-- Auto-loaded banner + expandable override form -->
    <div class="bg-white rounded-lg shadow mb-6">
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
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Current Rate (¢/kWh)</label>
            <input
              v-model.number="currentPlanCents"
              type="number"
              step="0.01"
              min="0"
              placeholder="e.g. 7.5"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p class="mt-0.5 text-xs text-gray-400">Marginal ¢/kWh of current plan</p>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Current Base Fee ($/mo)</label>
            <input
              v-model.number="currentPlanBaseFee"
              type="number"
              step="0.01"
              min="0"
              placeholder="e.g. 9.95"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-600 mb-1">Post-expiry Rate (¢/kWh)</label>
            <input
              v-model.number="postExpiryRateCents"
              type="number"
              step="0.5"
              min="0"
              placeholder="e.g. 20"
              class="w-full border border-gray-300 rounded px-3 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
            <p class="mt-0.5 text-xs text-gray-400">Assumed rate after new plan expires</p>
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
    <template v-if="sweeps.length > 0">

      <!-- Best recommendation banner -->
      <div v-if="bestOverall" class="bg-green-50 border border-green-200 rounded-lg px-4 py-3 mb-6 flex items-center gap-3">
        <span class="text-green-700 font-semibold text-sm shrink-0">Recommendation</span>
        <span class="text-green-800 text-sm">{{ recommendation }}</span>
      </div>

      <!-- Sweep chart -->
      <div class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <div class="px-4 py-3 border-b border-gray-100 flex items-center gap-3 flex-wrap">
          <h2 class="text-base font-semibold text-gray-700">{{ totalCostMode ? 'Total Cost' : 'Post-Switch Cost' }} by Switch Entry Date</h2>
          <span class="text-xs text-gray-400">Click a point to inspect that entry · ◆ = best entry per strategy</span>
          <div class="ml-auto flex items-center gap-3">
            <div class="inline-flex rounded border border-gray-300 overflow-hidden text-xs">
              <button
                @click="totalCostMode = false"
                :class="['px-3 py-1.5 transition-colors', !totalCostMode ? 'bg-blue-600 text-white' : 'bg-white text-gray-600 hover:bg-gray-50']"
              >Post-switch only</button>
              <button
                @click="totalCostMode = true"
                :class="['px-3 py-1.5 transition-colors border-l border-gray-300', totalCostMode ? 'bg-blue-600 text-white' : 'bg-white text-gray-600 hover:bg-gray-50']"
              >Total cost considered</button>
            </div>
            <button
              v-if="selectedStrategyId"
              @click="selectedStrategyId = null; selectedOffset = null"
              class="text-xs text-blue-600 underline"
            >Reset selection</button>
          </div>
        </div>
        <div class="p-4">
          <div style="height: 380px">
            <Line :data="sweepChartData" :options="sweepChartOptions" style="height: 380px" />
          </div>
          <p class="mt-2 text-xs text-gray-400">
            <template v-if="totalCostMode">Y-axis = pre-switch cost + ETF + 12-month post-switch cost.</template>
            <template v-else>Y-axis = 12-month post-switch cost only (apples-to-apples comparison).</template>
            ◆ = cheapest entry date for each strategy. Click to inspect.
          </p>
        </div>
      </div>

      <!-- Strategy best-entry summary table -->
      <div class="bg-white rounded-lg shadow overflow-hidden mb-6">
        <div class="px-4 py-3 border-b border-gray-100 flex items-center gap-2">
          <h2 class="text-base font-semibold text-gray-700">Best Entry per Strategy</h2>
          <span class="text-xs text-gray-400">Click a row to inspect the 12-month breakdown</span>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-gray-50 text-gray-500 text-xs uppercase">
                <th class="text-left px-4 py-3 font-medium">Strategy</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">Best Entry / Enroll By</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">Pre-switch</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">ETF</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">Post-switch (12m)</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">Total</th>
                <th class="text-right px-4 py-3 font-medium whitespace-nowrap">Saves vs variable</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="sweep in sweeps" :key="sweep.strategy_id">
                <tr
                  :class="[
                    'border-t border-gray-100 cursor-pointer transition-colors',
                    sweep.strategy_id === selectedStrategyId ? 'bg-blue-50' : 'hover:bg-gray-50',
                    sweep.strategy_id === bestOverall?.sweep.strategy_id ? 'bg-green-50 hover:bg-green-100' : '',
                    sweep.strategy_id === selectedStrategyId && sweep.strategy_id === bestOverall?.sweep.strategy_id ? 'bg-blue-50' : '',
                  ]"
                  @click="onRowClick(sweep.strategy_id)"
                >
                  <td class="px-4 py-3">
                    <div class="flex items-center gap-2">
                      <span class="inline-block w-3 h-3 rounded-full shrink-0"
                        :style="{ background: SWEEP_COLORS[sweep.strategy_id] ?? '#888' }" />
                      <span class="font-medium text-gray-900">{{ sweep.strategy_name }}</span>
                      <span v-if="sweep.strategy_id === 'variable'" class="text-xs text-gray-400">(baseline)</span>
                      <span v-if="sweep.strategy_id === bestOverall?.sweep.strategy_id"
                        class="ml-1 text-xs bg-green-600 text-white px-1.5 py-0.5 rounded">Best</span>
                      <span class="ml-auto text-gray-400 text-xs">
                        {{ sweep.strategy_id === selectedStrategyId ? '▲' : '▼' }}
                      </span>
                    </div>
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700">
                    {{ effectiveBestIndex(sweep) === 0 ? 'Now' : `+${sweep.entries[effectiveBestIndex(sweep)].weeks_from_today}w` }}
                    <span class="text-xs text-gray-400 ml-1">({{ sweep.entries[effectiveBestIndex(sweep)].window_start }})</span>
                    <template v-if="sweep.entries[effectiveBestIndex(sweep)].action_date_start">
                      <br>
                      <span class="text-xs text-blue-600">
                        enroll {{ sweep.entries[effectiveBestIndex(sweep)].action_date_start }} – {{ sweep.entries[effectiveBestIndex(sweep)].action_date_end }}
                      </span>
                    </template>
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-600">
                    {{ sweep.entries[effectiveBestIndex(sweep)].pre_switch_cost > 0
                      ? `$${sweep.entries[effectiveBestIndex(sweep)].pre_switch_cost.toFixed(2)}`
                      : '—' }}
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums">
                    <span :class="sweep.entries[effectiveBestIndex(sweep)].etf_applied > 0 ? 'text-red-600' : 'text-gray-400'">
                      {{ sweep.entries[effectiveBestIndex(sweep)].etf_applied > 0
                        ? `$${sweep.entries[effectiveBestIndex(sweep)].etf_applied.toFixed(2)}`
                        : '—' }}
                    </span>
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums text-gray-700">
                    ${{ sweep.entries[effectiveBestIndex(sweep)].post_switch_cost.toFixed(2) }}
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums font-semibold text-gray-900">
                    ${{ sweep.entries[effectiveBestIndex(sweep)].total_cost.toFixed(2) }}
                  </td>
                  <td class="px-4 py-3 text-right tabular-nums font-semibold">
                    <span :class="sweep.entries[effectiveBestIndex(sweep)].savings_vs_baseline > 0 ? 'text-green-700'
                      : sweep.entries[effectiveBestIndex(sweep)].savings_vs_baseline < 0 ? 'text-red-600'
                      : 'text-gray-400'">
                      {{ sweep.entries[effectiveBestIndex(sweep)].savings_vs_baseline > 0 ? '+' : '' }}${{
                        sweep.entries[effectiveBestIndex(sweep)].savings_vs_baseline.toFixed(2) }}
                    </span>
                  </td>
                </tr>

                <!-- Inline breakdown for selected strategy -->
                <tr v-if="sweep.strategy_id === selectedStrategyId" :key="sweep.strategy_id + '-breakdown'">
                  <td colspan="7" class="p-0 bg-blue-50 border-t border-blue-200">

                    <!-- Offset selector tabs -->
                    <div class="px-4 pb-1 flex items-center gap-1 flex-wrap">
                      <span class="text-xs text-gray-500 mr-2">Entry date:</span>
                      <button
                        v-for="(entry, idx) in sweep.entries"
                        :key="idx"
                        @click.stop="selectedOffset = idx"
                        :class="[
                          'px-2 py-0.5 text-xs rounded border transition-colors',
                          (selectedOffset === idx || (selectedOffset === null && idx === effectiveBestIndex(sweep)))
                            ? 'bg-blue-600 text-white border-blue-600'
                            : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50',
                          idx === effectiveBestIndex(sweep) ? 'font-bold' : '',
                        ]"
                      >
                        {{ idx === 0 ? 'Now' : `+${entry.weeks_from_today}w` }}
                        <span v-if="idx === effectiveBestIndex(sweep)" class="ml-0.5">★</span>
                      </button>
                    </div>

                    <!-- Cost summary row -->
                    <div v-if="selectedEntry" class="px-4 py-2 flex items-center gap-6 text-xs text-gray-600 border-b border-blue-100 flex-wrap">
                      <span>Enter: <strong>{{ selectedEntry.window_start }}</strong></span>
                      <span v-if="selectedEntry.action_date_start" class="text-blue-600">
                        Enroll: <strong>{{ selectedEntry.action_date_start }} – {{ selectedEntry.action_date_end }}</strong>
                      </span>
                      <template v-if="totalCostMode">
                        <template v-if="selectedEntry.pre_switch_cost > 0">
                          <span>Pre-switch: <strong>${{ selectedEntry.pre_switch_cost.toFixed(2) }}</strong></span>
                        </template>
                        <template v-if="selectedEntry.etf_applied > 0">
                          <span class="text-red-600">ETF: <strong>${{ selectedEntry.etf_applied.toFixed(2) }}</strong></span>
                        </template>
                      </template>
                      <span>Post-switch (12m): <strong>${{ selectedEntry.post_switch_cost.toFixed(2) }}</strong></span>
                      <template v-if="totalCostMode">
                        <span class="text-orange-600">Post-expiry (est.): <strong>${{ selectedEntryPostExpiryCost.toFixed(2) }}</strong></span>
                        <span>Total: <strong>${{ (selectedEntry.total_cost + selectedEntryPostExpiryCost).toFixed(2) }}</strong></span>
                      </template>
                      <template v-else>
                        <span>Total (12m): <strong>${{ selectedEntry.post_switch_cost.toFixed(2) }}</strong></span>
                      </template>
                                    </div>

                    <!-- Breakdown stacked bar chart -->
                    <div v-if="selectedEntry" class="px-4 pb-3 pt-2 border-b border-blue-100">
                      <div style="height: 240px">
                        <Bar :data="(breakdownChartData as any)" :options="(breakdownChartOptions as any)" style="height: 240px" />
                      </div>
                    </div>

                    <!-- Period breakdown table -->
                    <div v-if="selectedEntry" class="overflow-x-auto">
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
                          </tr>
                        </thead>
                        <tbody>
                          <tr
                            v-for="pb in selectedEntry.period_breakdown"
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
                              <span
                                v-if="pb.plan_kind === 'actual'"
                                class="ml-1 inline-block text-xs bg-green-100 text-green-700 px-1 rounded"
                                title="Live market rate — enrollment available"
                              >live</span>
                              <span
                                v-else-if="pb.plan_kind === 'projected'"
                                class="ml-1 inline-block text-xs bg-blue-100 text-blue-600 px-1 rounded"
                                title="Projected from historical rates (~1 year ago)"
                              >est</span>
                              <span
                                v-else-if="pb.plan_kind === 'fallback'"
                                class="ml-1 inline-block text-xs bg-amber-100 text-amber-700 px-1 rounded"
                                title="Fallback: most-recent historical rate used (no data in ideal window)"
                              >fallback</span>
                              <button
                                v-if="pb.active_plan.enroll_url && pb.plan_kind === 'actual'"
                                @click.stop="openEnrollModal(pb.active_plan, pb.period_start)"
                                class="ml-2 text-blue-600 hover:underline"
                              >Enroll</button>
                            </td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">{{ pb.active_plan.kwh1000_cents.toFixed(2) }}</td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">
                              {{ pb.rate_cents.toFixed(2) }}<span v-if="pb.plan_kind !== 'actual'" class="ml-0.5 text-gray-400" title="Estimated rate">~</span>
                            </td>
                            <td class="px-3 py-2 text-right tabular-nums text-gray-700">${{ pb.base_fee.toFixed(2) }}</td>
                            <td class="px-3 py-2 text-right tabular-nums font-medium text-gray-900">${{ pb.period_cost.toFixed(2) }}</td>
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
