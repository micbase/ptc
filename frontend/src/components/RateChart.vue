<script setup lang="ts">
import { computed, ref } from 'vue'
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS,
  LineElement,
  PointElement,
  LinearScale,
  TimeScale,
  Tooltip,
  Legend,
} from 'chart.js'
import 'chartjs-adapter-date-fns'
import ZoomPlugin from 'chartjs-plugin-zoom'
import type { ChartPoint } from '../types'

ChartJS.register(LineElement, PointElement, LinearScale, TimeScale, Tooltip, Legend, ZoomPlugin)

const props = defineProps<{
  best: ChartPoint[]
  best3m: ChartPoint[]
  variable: ChartPoint[]
  loading?: boolean
}>()

const lineRef = ref<InstanceType<typeof Line> | null>(null)

const data = computed(() => ({
  datasets: [
    {
      label: 'Best Plan Rate',
      data: props.best.map((p) => ({ x: p.fetch_date, y: p.kwh1000 })) as any,
      borderColor: '#7c3aed',
      backgroundColor: '#7c3aed',
      borderWidth: 2,
      pointRadius: 0,
      tension: 0.3,
    },
    {
      label: 'Best 3M Plan Rate',
      data: props.best3m.map((p) => ({ x: p.fetch_date, y: p.kwh1000 })) as any,
      borderColor: '#dc2626',
      backgroundColor: '#dc2626',
      borderWidth: 2,
      pointRadius: 0,
      tension: 0.3,
    },
    {
      label: 'Best Variable Rate',
      data: props.variable.map((p) => ({ x: p.fetch_date, y: p.kwh1000 })) as any,
      borderColor: '#16a34a',
      backgroundColor: '#16a34a',
      borderWidth: 2,
      pointRadius: 0,
      tension: 0.3,
    },
  ],
}))

const options = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    mode: 'index' as const,
    intersect: false,
  },
  scales: {
    x: {
      type: 'time' as const,
      time: {
        minUnit: 'day' as const,
        tooltipFormat: 'yyyy-MM-dd',
        displayFormats: {
          day: 'MM-dd',
          week: 'MM-dd',
          month: 'yyyy-MM',
          quarter: 'yyyy-MM',
          year: 'yyyy',
        },
      },
      ticks: {
        maxRotation: 45,
        autoSkip: true,
        maxTicksLimit: 12,
      },
    },
    y: {
      title: { display: true, text: '¢/kWh' },
    },
  },
  plugins: {
    legend: { position: 'top' as const },
    tooltip: { enabled: true },
    zoom: {
      zoom: {
        drag: {
          enabled: true,
          backgroundColor: 'rgba(114,164,233,0.2)',
          borderColor: 'rgba(114,164,233,0.8)',
          borderWidth: 1,
        },
        mode: 'x' as const,
      },
      limits: {
        x: { min: 'original' as const, max: 'original' as const },
      },
    },
  },
}))

function resetZoom() {
  // resetZoom is added by chartjs-plugin-zoom at runtime, not in Chart.js types
  ;(lineRef.value?.chart as any)?.resetZoom()
}
</script>

<template>
  <div class="relative" style="height: 450px">
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
      <svg class="animate-spin h-8 w-8 text-indigo-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
      </svg>
    </div>
    <button
      class="absolute top-1 right-1 z-20 px-2 py-0.5 text-xs text-gray-500 hover:text-gray-800 border border-gray-200 rounded bg-white/90 hover:bg-white"
      @click="resetZoom"
    >
      Reset zoom
    </button>
    <Line ref="lineRef" :data="data" :options="options" style="height: 450px" @dblclick="resetZoom" />
  </div>
</template>
