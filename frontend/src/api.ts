import type { ElectricityRate, ChartPoint, ProjectionRequest, StrategyResult } from './types'

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

export async function fetchProjection(req: ProjectionRequest): Promise<StrategyResult[]> {
  const res = await fetch(`${BASE}/projection`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}
