import type { ElectricityRate, ChartPoint, UsageMonth } from './types'

const BASE = '/api'

export async function fetchPlans(date: string): Promise<ElectricityRate[]> {
  const res = await fetch(`${BASE}/plans?date=${date}`)
  if (!res.ok) throw new Error(await res.text())
  return (await res.json()) ?? []
}

export async function fetchChartData(type: 'best' | 'best_3m' | 'variable'): Promise<ChartPoint[]> {
  const res = await fetch(`${BASE}/charts?type=${type}`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function fetchLatestDate(): Promise<string> {
  const res = await fetch(`${BASE}/latest-date`)
  if (!res.ok) throw new Error(await res.text())
  const data = await res.json()
  return data.date
}

export async function triggerFetch(): Promise<{ upserted: number; message: string }> {
  const res = await fetch(`${BASE}/fetch`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

export async function fetchUsageMonthly(): Promise<UsageMonth[]> {
  const res = await fetch(`${BASE}/usage/monthly`)
  if (!res.ok) throw new Error(await res.text())
  return (await res.json()) ?? []
}

export async function fetchUsageAvg(): Promise<{ avg_monthly_kwh: number }> {
  const res = await fetch(`${BASE}/usage/avg`)
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}
