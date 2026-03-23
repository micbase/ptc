<script setup lang="ts">
import { computed, ref, onMounted, onUnmounted, nextTick } from 'vue'
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

const GRID_LEFT = 60
const GRID_RIGHT = 20

const chartRef = ref()
const dragStart = ref<number | null>(null)
const dragCurrent = ref<number | null>(null)
const isDragging = ref(false)
let listenerDom: HTMLElement | null = null

const selectionStyle = computed(() => {
  if (!isDragging.value || dragStart.value === null || dragCurrent.value === null) {
    return { display: 'none' }
  }
  const left = Math.min(dragStart.value, dragCurrent.value)
  const width = Math.abs(dragCurrent.value - dragStart.value)
  return {
    position: 'absolute' as const,
    left: `${left}px`,
    top: '40px',
    width: `${width}px`,
    bottom: '50px',
    background: 'rgba(114, 164, 233, 0.2)',
    border: '1px solid rgba(114, 164, 233, 0.8)',
    pointerEvents: 'none' as const,
    zIndex: 20,
  }
})

function onMouseDown(e: MouseEvent) {
  const dom = listenerDom
  if (!dom) return
  const rect = dom.getBoundingClientRect()
  dragStart.value = e.clientX - rect.left
  dragCurrent.value = dragStart.value
  isDragging.value = false
}

function onMouseMove(e: MouseEvent) {
  if (dragStart.value === null) return
  const dom = listenerDom
  if (!dom) return
  const rect = dom.getBoundingClientRect()
  dragCurrent.value = e.clientX - rect.left
  if (Math.abs(dragCurrent.value - dragStart.value) > 4) isDragging.value = true
}

function onMouseUp() {
  if (isDragging.value && dragStart.value !== null && dragCurrent.value !== null) {
    const dom = listenerDom
    if (dom && chartRef.value) {
      const x1 = Math.min(dragStart.value, dragCurrent.value)
      const x2 = Math.max(dragStart.value, dragCurrent.value)

      const plotWidth = dom.clientWidth - GRID_LEFT - GRID_RIGHT
      const cx1 = Math.max(0, Math.min(plotWidth, x1 - GRID_LEFT))
      const cx2 = Math.max(0, Math.min(plotWidth, x2 - GRID_LEFT))

      // Get current dataZoom range so nested zooms work correctly
      const opts = chartRef.value.getOption() as any
      const dz = opts?.dataZoom?.[0]
      const curStart: number = dz?.start ?? 0
      const curEnd: number = dz?.end ?? 100

      const newStart = curStart + (cx1 / plotWidth) * (curEnd - curStart)
      const newEnd = curStart + (cx2 / plotWidth) * (curEnd - curStart)

      if (newEnd - newStart > 0.5) {
        chartRef.value.dispatchAction({ type: 'dataZoom', start: newStart, end: newEnd })
      }
    }
  }
  dragStart.value = null
  dragCurrent.value = null
  isDragging.value = false
}

function attachMouseDown() {
  const dom = chartRef.value?.getDom() as HTMLElement | null
  if (!dom || listenerDom === dom) return
  listenerDom?.removeEventListener('mousedown', onMouseDown)
  dom.addEventListener('mousedown', onMouseDown)
  listenerDom = dom
}

// Called by @finished on the chart — fires after first render
function onChartFinished() {
  attachMouseDown()
}

function resetZoom() {
  chartRef.value?.dispatchAction({ type: 'dataZoom', start: 0, end: 100 })
}

onMounted(() => {
  // Try immediately, and again after nextTick in case chart isn't ready yet
  nextTick(attachMouseDown)
  window.addEventListener('mousemove', onMouseMove)
  window.addEventListener('mouseup', onMouseUp)
})

onUnmounted(() => {
  listenerDom?.removeEventListener('mousedown', onMouseDown)
  window.removeEventListener('mousemove', onMouseMove)
  window.removeEventListener('mouseup', onMouseUp)
  listenerDom = null
})

const option = computed(() => ({
  tooltip: {
    trigger: 'axis',
  },
  legend: {
    top: 0,
    data: ['Best Plan Rate', 'Best 3M Plan Rate', 'Best Variable Rate'],
  },
  grid: {
    left: GRID_LEFT,
    right: GRID_RIGHT,
    top: 40,
    bottom: 50,
  },
  xAxis: {
    type: 'category',
    data: props.best.map((p) => p.fetch_date),
    axisLabel: {
      rotate: 45,
      formatter: (value: string) => {
        if (value.endsWith('-01')) return value.slice(0, 7)
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
    { type: 'inside', start: 0, end: 100, moveOnMouseMove: false },
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
  <div class="relative select-none" style="height: 450px; cursor: crosshair">
    <div v-if="loading" class="absolute inset-0 z-10 flex items-center justify-center bg-white/70">
      <svg class="animate-spin h-8 w-8 text-indigo-600" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24">
        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
      </svg>
    </div>
    <div :style="selectionStyle" />
    <button
      class="absolute top-1 right-1 z-30 px-2 py-0.5 text-xs text-gray-500 hover:text-gray-800 border border-gray-200 rounded bg-white/90 hover:bg-white"
      @click="resetZoom"
      @mousedown.stop
    >
      Reset zoom
    </button>
    <v-chart
      ref="chartRef"
      :option="option"
      autoresize
      style="height: 450px"
      @finished="onChartFinished"
    />
  </div>
</template>
