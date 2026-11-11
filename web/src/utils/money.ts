// 金额工具：界面内部用“分”的整数运算，与后端交互用十进制字符串。

/** 十进制字符串 → 分；无法解析返回 0。 */
export function toCents(s: string | number | undefined | null): number {
  if (s === undefined || s === null || s === '') return 0
  const n = Number(String(s).replace(/,/g, ''))
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

/** 分 → 后端用的十进制字符串。 */
export function fromCents(c: number): string {
  return (c / 100).toFixed(2)
}

/** 显示用：千分位、两位小数；零显示为空（账表惯例）。 */
export function fmt(s: string | number | undefined | null, zero = ''): string {
  const c = typeof s === 'number' ? s : toCents(s)
  if (c === 0) return zero
  return (c / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function fmtCents(c: number, zero = ''): string {
  return fmt(c, zero)
}

const DIGITS = '零壹贰叁肆伍陆柒捌玖'
const UNITS = ['', '拾', '佰', '仟']
const SECTIONS = ['', '万', '亿', '万亿']

/** 分 → 人民币大写。 */
export function toChinese(cents: number): string {
  if (cents === 0) return '零元整'
  const neg = cents < 0
  cents = Math.abs(cents)
  let yuan = Math.floor(cents / 100)
  const jiao = Math.floor((cents % 100) / 10)
  const fen = cents % 10
  let out = ''
  let sec = 0
  let needZero = false
  while (yuan > 0) {
    const part = yuan % 10000
    let s = ''
    let zero = false
    for (let i = 0, p = part; i < 4 && p > 0; i++, p = Math.floor(p / 10)) {
      const d = p % 10
      if (d === 0) {
        zero = s !== ''
      } else {
        s = DIGITS[d] + UNITS[i] + (zero ? '零' : '') + s
        zero = false
      }
    }
    if (part > 0) {
      out = s + SECTIONS[sec] + (needZero ? '零' : '') + out
      needZero = part < 1000
    } else if (out) {
      needZero = true
    }
    yuan = Math.floor(yuan / 10000)
    sec++
  }
  out = out.replace(/零+/g, '零').replace(/零$/, '')
  if (out) out += '元'
  if (jiao === 0 && fen === 0) return (neg ? '负' : '') + out + '整'
  if (jiao > 0) out += DIGITS[jiao] + '角'
  else if (out) out += '零'
  if (fen > 0) out += DIGITS[fen] + '分'
  return (neg ? '负' : '') + out
}
