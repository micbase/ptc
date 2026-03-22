<script setup lang="ts">
import { computed } from 'vue'
import VChart from 'vue-echarts'
import { use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { ChartPoint } from '../types'

use([LineChart, GridComponent, TooltipComponent, LegendComponent, DataZoomComponent, CanvasRenderer])

const props = defineProps<{
  best: ChartPoint[]
  best3m: ChartPoint[]
  variable: ChartPoint[]
  loading?: boolean
}>()

const option = computed(() => ({
  tooltip: {
    trigger: 'axis',
  },
  legend: {
    top: 0,
    data: ['Best Plan Rate', 'Best 3M Plan Rate', 'Best Variable Rate'],
  },
  grid: {
    left: 60,
    right: 20,
    top: 40,
    bottom: 70,
  },
  xAxis: {
    type: 'category',
    data: props.best.map((p) => p.fetch_date),
    axisLabel: {
      rotate: 45,
      formatter: (value: string) => {
        // Show label only on the first day of each month
        if (value.endsWith('-01')) {
          return value.slice(0, 7)
        }
        return ''
      },
      interval: 0,
    },
  },
  yAxis: {
    type: 'value',
    name: '¢/kWh',
    nameLocation: 'middle',
    nameGap: 45,
    axisLabel: { formatter: '{value}' },
  },
  dataZoom: [
    { type: 'inside', start: 0, end: 100 },
    { type: 'slider', start: 0, end: 100, bottom: 10 },
  ],
  series: [
    {
      name: 'Best Plan Rate',
      type: 'line',
      data: props.best.map((p) => p.kwh1000),
      itemStyle: { color: '#7c3aed' },
      symbol: 'none',
      smooth: true,
    },
    {
      name: 'Best 3M Plan Rate',
      type: 'line',
      data: props.best3m.map((p) => p.kwh1000),
      itemStyle: { color: '#dc2626' },
      symbol: 'none',
      smooth: true,
    },
    {
      name: 'Best Variable Rate',
      type: 'line',
      data: props.variable.map((p) => p.kwh1000),
      itemStyle: { color: '#16a34a' },
      symbol: 'none',
      smooth: true,
    },
  ],
}))
</script>

<template>
  <div class="relative" style="height: 450px">
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
      <svg class="animate-spin h-8 w-8 text-indigo-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
      </svg>
    </div>
    <v-chart :option="option" autoresize style="height: 450px" />
  </div>
</template>
