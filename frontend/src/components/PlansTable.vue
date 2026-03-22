<script setup lang="ts">
import { ref, computed } from 'vue'
import type { ElectricityRate } from '../types'

const props = defineProps<{
  plans: ElectricityRate[]
  loading?: boolean
}>()

type SortKey = keyof ElectricityRate

const sortKey = ref<SortKey>('kwh1000')
const sortAsc = ref(true)

function toggleSort(key: SortKey) {
  if (sortKey.value === key) {
    sortAsc.value = !sortAsc.value
  } else {
    sortKey.value = key
    sortAsc.value = true
  }
}

const sorted = computed(() => {
  const copy = [...(props.plans ?? [])]
  const dir = sortAsc.value ? 1 : -1
  copy.sort((a, b) => {
    const av = a[sortKey.value]
    const bv = b[sortKey.value]
    if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * dir
    return String(av).localeCompare(String(bv)) * dir
  })
  return copy
})

function sortIndicator(key: SortKey) {
  if (sortKey.value !== key) return ''
  return sortAsc.value ? ' ▲' : ' ▼'
}

const columns: { key: SortKey; label: string; type: 'string' | 'number' | 'boolean' | 'link' | 'tag' | 'date' }[] = [
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
</script>

<template>
  <div class="relative overflow-x-auto">
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
          v-for="plan in sorted"
          :key="plan.id"
          class="border-b border-gray-100 hover:bg-gray-50"
        >
          <td
            v-for="col in columns"
            :key="col.key"
            :class="col.key === 'special_terms'
              ? 'px-3 py-2 max-w-xs overflow-hidden'
              : 'px-3 py-2 whitespace-nowrap'"
          >
            <!-- Link columns -->
            <template v-if="col.type === 'link'">
              <a
                v-if="plan[col.key]"
                :href="String(plan[col.key])"
                target="_blank"
                rel="noopener"
                class="text-blue-600 hover:underline"
              >Link</a>
            </template>
            <!-- Boolean columns -->
            <template v-else-if="col.type === 'boolean'">
              <span v-if="plan[col.key]" class="text-green-600">Yes</span>
              <span v-else class="text-gray-400">No</span>
            </template>
            <!-- Tag: rate_type -->
            <template v-else-if="col.key === 'rate_type'">
              <span class="px-2 py-0.5 rounded-full text-xs font-medium" :class="rateTypeBadge(String(plan[col.key]))">
                {{ plan[col.key] }}
              </span>
            </template>
            <!-- Tag: language -->
            <template v-else-if="col.key === 'language'">
              <span class="px-2 py-0.5 rounded-full text-xs font-medium" :class="languageBadge(String(plan[col.key]))">
                {{ plan[col.key] }}
              </span>
            </template>
            <!-- Number columns -->
            <template v-else-if="col.type === 'number'">
              <span class="tabular-nums" :class="{ 'font-semibold': col.key === 'kwh1000' }">{{ plan[col.key] }}</span>
            </template>
            <!-- Default: string/date -->
            <template v-else>
              <span
                :title="String(plan[col.key] ?? '')"
                :class="col.key === 'special_terms' ? 'block truncate' : ''"
              >{{ plan[col.key] }}</span>
            </template>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-if="(plans ?? []).length === 0" class="text-center text-gray-500 py-8">No plans found for this date.</p>
  </div>
</template>
