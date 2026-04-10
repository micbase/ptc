<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type { ElectricityRate } from '../types'

const props = defineProps<{
  plans: ElectricityRate[]
  loading?: boolean
  userKwh?: number
  monthsRemaining?: number
  currentPlanIdKey?: string
}>()

const emit = defineEmits<{
  'update:currentPlanIdKey': [value: string]
}>()

// Filter toggles — persisted to localStorage, default ON
const hideUsageCredit = ref(localStorage.getItem('ptc_hide_usage_credit') !== 'false')
const hideTOU = ref(localStorage.getItem('ptc_hide_tou') !== 'false')
watch(hideUsageCredit, v => localStorage.setItem('ptc_hide_usage_credit', String(v)))
watch(hideTOU, v => localStorage.setItem('ptc_hide_tou', String(v)))

// ── ETF parser ────────────────────────────────────────────────────────────────
function parseETF(cancelFee: string | null | undefined): { flat: number; perMonth: number } {
  const s = (cancelFee ?? '').trim()
  if (!s || s === 'None' || s === '0') return { flat: 0, perMonth: 0 }
  // "$9.95 per remaining month" | "$9.95/month" | "$9.95/mo"
  const perMonthMatch = s.match(/\$?([\d.]+)\s*(?:per remaining month|\/month|\/mo)\b/i)
  if (perMonthMatch) return { flat: 0, perMonth: parseFloat(perMonthMatch[1]) }
  // "$150" or "$150.00"
  const flatMatch = s.match(/^\$?([\d.]+)$/)
  if (flatMatch) return { flat: parseFloat(flatMatch[1]), perMonth: 0 }
  return { flat: 0, perMonth: 0 }
}

function formatCancelFee(cancelFee: string | null | undefined): string {
  const s = (cancelFee ?? '').trim()
  if (!s || s === 'None' || s === '0') return 'None'
  const etf = parseETF(cancelFee)
  if (etf.flat > 0) return `$${etf.flat} flat`
  if (etf.perMonth > 0) return `$${etf.perMonth}/mo`
  return s
}

// ── Cost estimator ────────────────────────────────────────────────────────────
function estimateCost(plan: ElectricityRate, kwh: number): { est_monthly_dollars: number; est_cents_per_kwh: number } | null {
  if (!kwh || plan.kwh500 == null || plan.kwh1000 == null || plan.kwh2000 == null) return null
  const C500  = plan.kwh500  * 500
  const C1000 = plan.kwh1000 * 1000
  const C2000 = plan.kwh2000 * 2000
  const n = 3, Sx = 3500, Sx2 = 5_250_000
  const Sy  = C500 + C1000 + C2000
  const Sxy = 500 * C500 + 1000 * C1000 + 2000 * C2000
  const rate     = (n * Sxy - Sx * Sy) / (n * Sx2 - Sx * Sx)
  const base_fee = (Sy - rate * Sx) / n
  const est_monthly_dollars = (base_fee + rate * kwh) / 100
  const est_cents_per_kwh   = est_monthly_dollars * 100 / kwh
  return { est_monthly_dollars, est_cents_per_kwh }
}

// ── Sort ──────────────────────────────────────────────────────────────────────
type ComputedKey = '_est_cents' | '_est_monthly' | '_total_etf' | '_payback'
type SortKey = keyof ElectricityRate | ComputedKey

const sortKey = ref<SortKey>('kwh1000')
const sortAsc = ref(true)

// Auto-switch sort when userKwh is first set
watch(() => props.userKwh, kwh => {
  if (kwh && kwh > 0 && sortKey.value === 'kwh1000') {
    sortKey.value = '_est_cents'
    sortAsc.value = true
  } else if ((!kwh || kwh === 0) && (sortKey.value === '_est_cents' || sortKey.value === '_est_monthly')) {
    sortKey.value = 'kwh1000'
    sortAsc.value = true
  }
})

function toggleSort(key: SortKey) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = true
  }
}

function sortIndicator(key: SortKey) {
  return sortKey.value === key ? (sortAsc.value ? ' ▲' : ' ▼') : ''
}

// ── Enriched plans ────────────────────────────────────────────────────────────
interface EnrichedPlan {
  plan: ElectricityRate
  etf: { flat: number; perMonth: number }
  estimate: { est_monthly_dollars: number; est_cents_per_kwh: number } | null
  totalEtf: number
}

const filtered = computed(() =>
  (props.plans ?? []).filter(p => {
    if (hideUsageCredit.value && p.min_usage_fees_credits) return false
    if (hideTOU.value && p.time_of_use) return false
    return true
  })
)

const enriched = computed((): EnrichedPlan[] => {
  const kwh = props.userKwh ?? 0
  const months = props.monthsRemaining ?? 0
  return filtered.value.map(plan => {
    const etf = parseETF(plan.cancel_fee)
    const estimate = kwh > 0 ? estimateCost(plan, kwh) : null
    const totalEtf = etf.flat > 0 ? etf.flat : etf.perMonth * months
    return { plan, etf, estimate, totalEtf }
  })
})

// ── Current plan ──────────────────────────────────────────────────────────────
function isCurrentPlan(idKey: string | null | undefined): boolean {
  return !!idKey && !!props.currentPlanIdKey && idKey === props.currentPlanIdKey
}

function toggleCurrentPlan(idKey: string | null | undefined) {
  if (!idKey) return
  emit('update:currentPlanIdKey', isCurrentPlan(idKey) ? '' : idKey)
}

const currentEnriched = computed((): EnrichedPlan | null =>
  enriched.value.find(e => e.plan.id_key === props.currentPlanIdKey) ?? null
)

// ── Payback ───────────────────────────────────────────────────────────────────
function paybackText(ep: EnrichedPlan): string {
  if (!props.currentPlanIdKey || !props.userKwh || props.userKwh === 0) return '—'
  if (isCurrentPlan(ep.plan.id_key)) return '—'
  const curr = currentEnriched.value
  if (!curr?.estimate || !ep.estimate) return '—'
  const savings = curr.estimate.est_monthly_dollars - ep.estimate.est_monthly_dollars
  if (savings <= 0) return '—'
  if (ep.totalEtf === 0) return 'Now'
  return (ep.totalEtf / savings).toFixed(1)
}

function paybackNumeric(ep: EnrichedPlan): number {
  const t = paybackText(ep)
  if (t === '—') return Infinity
  if (t === 'Now') return 0
  return parseFloat(t)
}

// ── Sorted ────────────────────────────────────────────────────────────────────
const filteredSorted = computed(() => {
  const copy = [...enriched.value]
  const dir = sortAsc.value ? 1 : -1
  copy.sort((a, b) => {
    switch (sortKey.value) {
      case '_est_cents':
        return ((a.estimate?.est_cents_per_kwh ?? Infinity) - (b.estimate?.est_cents_per_kwh ?? Infinity)) * dir
      case '_est_monthly':
        return ((a.estimate?.est_monthly_dollars ?? Infinity) - (b.estimate?.est_monthly_dollars ?? Infinity)) * dir
      case '_total_etf':
        return (a.totalEtf - b.totalEtf) * dir
      case '_payback':
        return (paybackNumeric(a) - paybackNumeric(b)) * dir
      default: {
        const k = sortKey.value as keyof ElectricityRate
        const av = a.plan[k]
        const bv = b.plan[k]
        if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * dir
        return String(av ?? '').localeCompare(String(bv ?? '')) * dir
      }
    }
  })
  return copy
})

// ── Recommendation card ───────────────────────────────────────────────────────
const recommendation = computed(() => {
  const kwh = props.userKwh ?? 0
  if (!kwh || !props.currentPlanIdKey) return null
  const curr = currentEnriched.value
  if (!curr?.estimate) return null
  const nonCurrent = filteredSorted.value.filter(e => !isCurrentPlan(e.plan.id_key) && e.estimate != null)
  if (nonCurrent.length === 0) return { currentIsBest: true, best: null, savings: 0 }
  const best = nonCurrent.reduce((a, b) =>
    a.estimate!.est_monthly_dollars < b.estimate!.est_monthly_dollars ? a : b
  )
  const savings = curr.estimate.est_monthly_dollars - best.estimate!.est_monthly_dollars
  if (savings <= 0) return { currentIsBest: true, best, savings }
  return { currentIsBest: false, best, savings }
})

// ── Existing columns ──────────────────────────────────────────────────────────
const columns: { key: keyof ElectricityRate; label: string; type: 'string' | 'number' | 'boolean' | 'link' | 'tag' | 'date' }[] = [
  { key: 'fetch_date', label: 'Fetch Date', type: 'date' },
  { key: 'tdu_company_name', label: 'TDU Company', type: 'string' },
  { key: 'rep_company', label: 'Rep Company', type: 'string' },
  { key: 'product', label: 'Product', type: 'string' },
  { key: 'kwh500', label: 'kWh 500', type: 'number' },
  { key: 'kwh1000', label: 'kWh 1000', type: 'number' },
  { key: 'kwh2000', label: 'kWh 2000', type: 'number' },
  { key: 'fees_credits', label: 'Fees/Credits', type: 'string' },
  { key: 'prepaid', label: 'Prepaid', type: 'boolean' },
  { key: 'time_of_use', label: 'Time of Use', type: 'boolean' },
  { key: 'fixed', label: 'Fixed', type: 'number' },
  { key: 'rate_type', label: 'Rate Type', type: 'tag' },
  { key: 'renewable', label: 'Renewable', type: 'number' },
  { key: 'term_value', label: 'Term', type: 'number' },
  { key: 'cancel_fee', label: 'Cancel Fee', type: 'string' },
  { key: 'website', label: 'Website', type: 'link' },
  { key: 'special_terms', label: 'Special Terms', type: 'string' },
  { key: 'terms_url', label: 'Terms URL', type: 'link' },
  { key: 'yrac_url', label: 'YRAC URL', type: 'link' },
  { key: 'promotion', label: 'Promotion', type: 'boolean' },
  { key: 'promotion_desc', label: 'Promotion Desc', type: 'string' },
  { key: 'facts_url', label: 'Facts URL', type: 'link' },
  { key: 'enroll_url', label: 'Enroll URL', type: 'link' },
  { key: 'prepaid_url', label: 'Prepaid URL', type: 'link' },
  { key: 'enroll_phone', label: 'Enroll Phone', type: 'string' },
  { key: 'new_customer', label: 'New Customer', type: 'boolean' },
  { key: 'min_usage_fees_credits', label: 'Min Usage Fees/Credits', type: 'boolean' },
  { key: 'language', label: 'Language', type: 'tag' },
  { key: 'rating', label: 'Rating', type: 'number' },
]

function rateTypeBadge(type: string) {
  if (type === 'Fixed') return 'bg-blue-100 text-blue-800'
  if (type === 'Variable') return 'bg-amber-100 text-amber-800'
  return 'bg-gray-100 text-gray-800'
}

function languageBadge(lang: string) {
  if (lang === 'English') return 'bg-green-100 text-green-800'
  if (lang === 'Spanish') return 'bg-purple-100 text-purple-800'
  return 'bg-gray-100 text-gray-800'
}

const hasKwh = computed(() => (props.userKwh ?? 0) > 0)
const hasCurrentPlan = computed(() => !!props.currentPlanIdKey)
</script>

<template>
  <div>
    <!-- Filter toggles -->
    <div class="flex flex-wrap gap-4 px-3 py-2 border-b border-gray-100 bg-gray-50 text-sm">
      <label class="flex items-center gap-2 cursor-pointer select-none text-gray-700">
        <input type="checkbox" v-model="hideUsageCredit" class="rounded" />
        Hide usage-credit plans
      </label>
      <label class="flex items-center gap-2 cursor-pointer select-none text-gray-700">
        <input type="checkbox" v-model="hideTOU" class="rounded" />
        Hide time-of-use plans
      </label>
    </div>

    <!-- Recommendation card -->
    <div v-if="recommendation" class="mx-3 mt-3 px-4 py-3 rounded-lg border text-sm"
      :class="recommendation.currentIsBest
        ? 'bg-green-50 border-green-200 text-green-800'
        : 'bg-blue-50 border-blue-200 text-blue-800'">
      <template v-if="recommendation.currentIsBest">
        You're on the best available plan.
      </template>
      <template v-else-if="recommendation.best">
        <span class="font-semibold">Best option:</span>
        {{ recommendation.best.plan.product }} by {{ recommendation.best.plan.rep_company }}
        — saves <span class="font-semibold">${{ recommendation.savings.toFixed(2) }}/mo</span>.
        <template v-if="recommendation.best.totalEtf > 0">
          ETF paid off in {{ (recommendation.best.totalEtf / recommendation.savings).toFixed(1) }} months.
        </template>
        <template v-else>
          No cancellation fee.
        </template>
      </template>
    </div>

    <!-- Table -->
    <div class="relative overflow-x-auto mt-3">
      <!-- Loading overlay -->
      <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
        <svg class="animate-spin h-8 w-8 text-indigo-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
        </svg>
      </div>

      <table class="min-w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 bg-gray-50">
            <!-- New columns first -->
            <th class="px-3 py-2 text-left font-medium text-gray-600 whitespace-nowrap">Current</th>
            <th v-if="hasKwh"
              class="px-3 py-2 text-left font-medium text-gray-600 cursor-pointer select-none whitespace-nowrap hover:text-gray-900"
              @click="toggleSort('_est_cents')">
              Est. ¢/kWh{{ sortIndicator('_est_cents') }}
            </th>
            <th v-if="hasKwh"
              class="px-3 py-2 text-left font-medium text-gray-600 cursor-pointer select-none whitespace-nowrap hover:text-gray-900"
              @click="toggleSort('_est_monthly')">
              Est. Monthly ($){{ sortIndicator('_est_monthly') }}
            </th>
            <th v-if="hasKwh && hasCurrentPlan"
              class="px-3 py-2 text-left font-medium text-gray-600 cursor-pointer select-none whitespace-nowrap hover:text-gray-900"
              @click="toggleSort('_payback')">
              Payback (mo){{ sortIndicator('_payback') }}
            </th>
            <th
              class="px-3 py-2 text-left font-medium text-gray-600 cursor-pointer select-none whitespace-nowrap hover:text-gray-900"
              @click="toggleSort('_total_etf')">
              Total ETF{{ sortIndicator('_total_etf') }}
            </th>
            <!-- Existing columns -->
            <th
              v-for="col in columns"
              :key="col.key"
              class="px-3 py-2 text-left font-medium text-gray-600 cursor-pointer select-none whitespace-nowrap hover:text-gray-900"
              @click="toggleSort(col.key)"
            >
              {{ col.label }}{{ sortIndicator(col.key) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="ep in filteredSorted"
            :key="ep.plan.id"
            class="border-b border-gray-100"
            :class="isCurrentPlan(ep.plan.id_key)
              ? 'bg-yellow-50 ring-1 ring-inset ring-yellow-200'
              : 'hover:bg-gray-50'"
          >
            <!-- Current toggle -->
            <td class="px-3 py-2 whitespace-nowrap">
              <button
                @click="toggleCurrentPlan(ep.plan.id_key)"
                class="text-lg leading-none transition-colors"
                :class="isCurrentPlan(ep.plan.id_key) ? 'text-yellow-500' : 'text-gray-300 hover:text-gray-500'"
                :title="isCurrentPlan(ep.plan.id_key) ? 'Unmark as current plan' : 'Mark as my current plan'"
              >
                {{ isCurrentPlan(ep.plan.id_key) ? '★' : '☆' }}
              </button>
            </td>

            <!-- Est. ¢/kWh -->
            <td v-if="hasKwh" class="px-3 py-2 whitespace-nowrap tabular-nums">
              {{ ep.estimate ? ep.estimate.est_cents_per_kwh.toFixed(1) + '¢' : '—' }}
            </td>

            <!-- Est. Monthly ($) -->
            <td v-if="hasKwh" class="px-3 py-2 whitespace-nowrap tabular-nums">
              {{ ep.estimate ? '$' + ep.estimate.est_monthly_dollars.toFixed(2) : '—' }}
            </td>

            <!-- Payback (mo) -->
            <td v-if="hasKwh && hasCurrentPlan" class="px-3 py-2 whitespace-nowrap tabular-nums">
              {{ paybackText(ep) }}
            </td>

            <!-- Total ETF -->
            <td class="px-3 py-2 whitespace-nowrap tabular-nums">
              {{ ep.totalEtf > 0 ? '$' + ep.totalEtf.toFixed(0) : '—' }}
            </td>

            <!-- Existing columns -->
            <td
              v-for="col in columns"
              :key="col.key"
              :class="(col.type === 'string' || col.type === 'date')
                ? 'px-3 py-2 max-w-xs overflow-hidden'
                : 'px-3 py-2 whitespace-nowrap'"
            >
              <!-- Cancel fee: structured display -->
              <template v-if="col.key === 'cancel_fee'">
                <span class="text-gray-700">{{ formatCancelFee(ep.plan.cancel_fee) }}</span>
              </template>
              <!-- Link columns -->
              <template v-else-if="col.type === 'link'">
                <a
                  v-if="ep.plan[col.key]"
                  :href="String(ep.plan[col.key])"
                  target="_blank"
                  rel="noopener"
                  class="text-blue-600 hover:underline"
                >Link</a>
              </template>
              <!-- Boolean columns -->
              <template v-else-if="col.type === 'boolean'">
                <span v-if="ep.plan[col.key]" class="text-green-600">Yes</span>
                <span v-else class="text-gray-400">No</span>
              </template>
              <!-- Tag: rate_type -->
              <template v-else-if="col.key === 'rate_type'">
                <span class="px-2 py-0.5 rounded-full text-xs font-medium" :class="rateTypeBadge(String(ep.plan[col.key]))">
                  {{ ep.plan[col.key] }}
                </span>
              </template>
              <!-- Tag: language -->
              <template v-else-if="col.key === 'language'">
                <span class="px-2 py-0.5 rounded-full text-xs font-medium" :class="languageBadge(String(ep.plan[col.key]))">
                  {{ ep.plan[col.key] }}
                </span>
              </template>
              <!-- Number columns -->
              <template v-else-if="col.type === 'number'">
                <span class="tabular-nums" :class="{ 'font-semibold': col.key === 'kwh1000' }">{{ ep.plan[col.key] }}</span>
              </template>
              <!-- Default: string/date -->
              <template v-else>
                <span class="block truncate" :title="String(ep.plan[col.key] ?? '')">{{ ep.plan[col.key] }}</span>
              </template>
            </td>
          </tr>
        </tbody>
      </table>

      <p v-if="filteredSorted.length === 0 && !loading" class="text-center text-gray-500 py-8">
        No plans found for this date.
      </p>
    </div>
  </div>
</template>
