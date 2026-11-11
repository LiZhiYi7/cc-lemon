// Excel 读写。exceljs 体积较大，按需加载。
import type { Workbook, Worksheet } from 'exceljs'

async function newWorkbook(): Promise<Workbook> {
  const ExcelJS = (await import('exceljs')).default
  return new ExcelJS.Workbook()
}

async function download(wb: Workbook, filename: string) {
  const buf = await wb.xlsx.writeBuffer()
  const url = URL.createObjectURL(new Blob([buf], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename.replace(/[\\/:*?"<>|]/g, '_') + '.xlsx'
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 显示宽度：中文按 2 个字符计。 */
const width = (s: string) => [...s].reduce((n, ch) => n + (ch.charCodeAt(0) > 255 ? 2 : 1), 0)

function autoWidth(ws: Worksheet, from = 1) {
  const max: number[] = []
  ws.eachRow((row, r) => {
    if (r < from) return
    row.eachCell((cell, c) => {
      const text = typeof cell.value === 'number' ? cell.value.toFixed(2) : String(cell.value ?? '')
      max[c] = Math.max(max[c] ?? 0, width(text))
    })
  })
  max.forEach((w, c) => (ws.getColumn(c).width = Math.min(Math.max(w + 3, 8), 60)))
}

const THIN = { style: 'thin' as const, color: { argb: 'FFBFC5D2' } }
const BORDER = { top: THIN, left: THIN, bottom: THIN, right: THIN }
const HEAD_FILL = { type: 'pattern' as const, pattern: 'solid' as const, fgColor: { argb: 'FFF1F4F9' } }

/**
 * 把页面上的账表（table.sheet）原样导出：保留多行表头与合并单元格、金额为数值、缩进与加粗。
 * 表头为空或为“操作”的列不导出。
 */
export async function exportTable(table: HTMLTableElement, filename: string, title?: string, subtitle?: string) {
  interface Cell { text: string; num?: number; bold: boolean; indent: number; center: boolean; head: boolean }
  const grid: (Cell | undefined)[][] = []
  const merges: [number, number, number, number][] = []
  const headRows = table.tHead?.rows.length ?? 0
  const occupied = new Set<string>()
  Array.from(table.rows).forEach((tr, r) => {
    grid[r] = grid[r] ?? []
    const foot = tr.parentElement?.tagName === 'TFOOT'
    let c = 0
    for (const td of Array.from(tr.cells)) {
      while (occupied.has(`${r},${c}`)) c++
      const text = (td.innerText ?? '').replace(/\s+/g, ' ').trim()
      const cell: Cell = {
        text,
        bold: foot || r < headRows || tr.classList.contains('bold') || td.classList.contains('b'),
        indent: Math.max(0, Math.round((parseFloat(td.style.paddingLeft || '0') - 10) / 16)),
        center: td.classList.contains('center') || r < headRows,
        head: r < headRows,
      }
      if (td.classList.contains('num') && /^-?[\d,]+(\.\d+)?$/.test(text)) cell.num = Number(text.replace(/,/g, ''))
      for (let dr = 0; dr < td.rowSpan; dr++) for (let dc = 0; dc < td.colSpan; dc++) occupied.add(`${r + dr},${c + dc}`)
      grid[r][c] = cell
      if (td.rowSpan > 1 || td.colSpan > 1) merges.push([r, c, r + td.rowSpan - 1, c + td.colSpan - 1])
      c += td.colSpan
    }
  })
  const cols = Math.max(...grid.map((row) => row.length))
  // 每列最底层的表头文字，用于判断是否导出
  const keep: number[] = []
  for (let c = 0; c < cols; c++) {
    let label = ''
    for (let r = headRows - 1; r >= 0 && !label; r--) label = grid[r]?.[c]?.text ?? ''
    if (headRows === 0 || (label && label !== '操作')) keep.push(c)
  }
  const colMap = new Map(keep.map((c, i) => [c, i + 1]))

  const wb = await newWorkbook()
  const ws = wb.addWorksheet((title || '账表').slice(0, 30))
  let offset = 0
  for (const [text, size] of [[title, 14], [subtitle, 10]] as const) {
    if (!text) continue
    offset++
    ws.mergeCells(offset, 1, offset, keep.length)
    const cell = ws.getCell(offset, 1)
    cell.value = text
    cell.font = { bold: size > 10, size }
    cell.alignment = { horizontal: 'center', vertical: 'middle' }
  }
  grid.forEach((row, r) => {
    for (const c of keep) {
      const out = ws.getCell(r + 1 + offset, colMap.get(c)!)
      out.border = BORDER
      const cell = row[c]
      if (!cell) continue
      out.value = cell.num ?? cell.text
      if (cell.num !== undefined) out.numFmt = '#,##0.00;-#,##0.00'
      out.font = { bold: cell.bold }
      out.alignment = { horizontal: cell.num !== undefined ? 'right' : cell.center ? 'center' : 'left', vertical: 'middle', indent: cell.indent, wrapText: cell.head }
      if (cell.head) out.fill = HEAD_FILL
    }
  })
  for (const [r1, c1, r2, c2] of merges) {
    const kept = keep.filter((c) => c >= c1 && c <= c2).map((c) => colMap.get(c)!)
    if (!kept.length || (r1 === r2 && kept.length === 1)) continue
    ws.mergeCells(r1 + 1 + offset, kept[0], r2 + 1 + offset, kept[kept.length - 1])
  }
  autoWidth(ws, offset + 1)
  ws.views = [{ state: 'frozen', ySplit: offset + headRows }]
  await download(wb, filename)
}

/** 写一张简单的数据表（导出档案、导入模板）。字符串按文本格式写入，保留编码前导零。 */
export async function writeSheet(filename: string, headers: string[], rows: (string | number)[][], note?: string) {
  const wb = await newWorkbook()
  const ws = wb.addWorksheet('数据')
  ws.addRow(headers)
  ws.getRow(1).eachCell((cell) => {
    cell.font = { bold: true }
    cell.fill = HEAD_FILL
    cell.border = BORDER
    cell.alignment = { horizontal: 'center' }
  })
  for (const r of rows) {
    const row = ws.addRow(r)
    row.eachCell((cell) => {
      if (typeof cell.value === 'number') cell.numFmt = '#,##0.00;-#,##0.00'
      else cell.numFmt = '@'
    })
  }
  autoWidth(ws)
  ws.views = [{ state: 'frozen', ySplit: 1 }]
  if (note) {
    const help = wb.addWorksheet('填写说明')
    note.split('\n').forEach((line) => help.addRow([line]))
    help.getColumn(1).width = 100
  }
  await download(wb, filename)
}

export interface SheetRow { row: number; values: Record<string, string> }

/** 读取第一张工作表：首行为表头，返回每个非空数据行（row 为 Excel 行号）。 */
export async function readSheet(file: File): Promise<{ headers: string[]; rows: SheetRow[] }> {
  const wb = await newWorkbook()
  await wb.xlsx.load(await file.arrayBuffer())
  const ws = wb.worksheets[0]
  if (!ws) throw new Error('文件中没有工作表')
  const text = (v: unknown): string => {
    if (v === null || v === undefined) return ''
    if (typeof v === 'object') {
      const o = v as { result?: unknown; richText?: { text: string }[]; text?: string }
      if (o.richText) return o.richText.map((t) => t.text).join('')
      if (o.result !== undefined) return text(o.result)
      if (o.text !== undefined) return String(o.text)
      return ''
    }
    // 数值去掉浮点尾差
    return typeof v === 'number' ? String(Math.round(v * 1e6) / 1e6) : String(v)
  }
  const headers: string[] = []
  ws.getRow(1).eachCell((cell, c) => (headers[c] = text(cell.value).trim()))
  const rows: SheetRow[] = []
  ws.eachRow((row, r) => {
    if (r === 1) return
    const values: Record<string, string> = {}
    let any = false
    row.eachCell((cell, c) => {
      const v = text(cell.value).trim()
      if (headers[c] && v) {
        values[headers[c]] = v
        any = true
      }
    })
    if (any) rows.push({ row: r, values })
  })
  return { headers: headers.filter(Boolean), rows }
}
