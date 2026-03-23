import type { ElectricityRate, ChartPoint } from './types'

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

export async function triggerFetch(force = false): Promise<{ inserted: number; skipped: boolean; message: string }> {
  const res = await fetch(`${BASE}/fetch${force ? '?force=true' : ''}`, { method: 'POST' })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}
