import type { Period } from '@/api/types'

export const periodStr = (p: Period) => `${p.year}-${String(p.month).padStart(2, '0')}`

export function parsePeriod(s: string): Period {
  const [y, m] = s.split('-').map(Number)
  return { year: y, month: m }
}

/** 期间内的默认凭证日期：当月取今天，否则取月末。 */
export function defaultDate(p: Period): string {
  const now = new Date()
  const day =
    now.getFullYear() === p.year && now.getMonth() + 1 === p.month ? now.getDate() : new Date(p.year, p.month, 0).getDate()
  return `${periodStr(p)}-${String(day).padStart(2, '0')}`
}
