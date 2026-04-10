<script setup lang="ts">
import type { UsageMonth } from '../types'

defineProps<{
  months: UsageMonth[]
  avgKwh: number
  loading: boolean
}>()

function formatMonth(m: string): string {
  const d = new Date(m + 'T00:00:00')
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short' })
}
</script>

<template>
  <div class="relative">
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
      <svg class="animate-spin h-6 w-6 text-indigo-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
      </svg>
    </div>
    <div class="flex items-center justify-between mb-3">
      <h2 class="text-base font-semibold text-gray-800">Monthly Usage</h2>
      <span v-if="avgKwh > 0" class="text-sm text-gray-600">
        Avg: <span class="font-semibold tabular-nums">{{ avgKwh.toFixed(0) }} kWh/mo</span>
        <span class="text-gray-400 ml-1">(last 12 months)</span>
      </span>
    </div>
    <div v-if="months.length === 0 && !loading" class="text-sm text-gray-400 py-2">
      No usage data available.
    </div>
    <div v-else class="overflow-x-auto">
      <table class="text-sm">
        <thead>
          <tr class="border-b border-gray-200">
            <th class="px-3 py-1.5 text-left font-medium text-gray-600">Month</th>
            <th class="px-3 py-1.5 text-right font-medium text-gray-600">Total kWh</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="m in months" :key="m.month" class="border-b border-gray-100 hover:bg-gray-50">
            <td class="px-3 py-1.5 text-gray-700">{{ formatMonth(m.month) }}</td>
            <td class="px-3 py-1.5 text-right tabular-nums text-gray-900">{{ m.total_kwh.toFixed(1) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
