// 与后端 JSON 对应的类型。金额一律为字符串（十进制），避免浮点误差。

export interface User { id: number; username: string; display_name: string; is_admin: boolean; enabled: boolean }
export interface Period { year: number; month: number }
export interface Standard { code: string; name: string; description: string; ledgers: { code: string; name: string }[] }
export interface Role { code: string; name: string; permissions: string[] }

export interface BookSettings {
  account_code_scheme: number[]
  voucher: { reviewer_must_differ: boolean; require_review_to_close: boolean }
  parallel: { mode: 'off' | 'warn' | 'strict' }
}
export interface Book {
  id: number; name: string; company: string; standard_code: string; standard_name: string
  start_year: number; start_month: number; settings: BookSettings; current_period: Period; role: string
}
export interface Ledger { id: number; code: string; name: string; is_primary: boolean }
export interface Category { ledger_id: number; code: string; name: string; is_pl: boolean }
export interface VoucherWord { word: string; title: string; is_default: boolean }
export interface BookDetail extends Book {
  ledgers: Ledger[]; categories: Category[]; voucher_words: VoucherWord[]; permissions: string[]
}
export interface Member { user_id: number; username: string; display_name: string; role: string }

export interface Account {
  id: number; ledger_id: number; code: string; name: string; full_name: string; parent_id: number | null
  level: number; category: string; direction: 'D' | 'C'; is_leaf: boolean; is_cash: boolean; enabled: boolean; is_system: boolean
}
export interface Opening { account_id: number; balance: string; ytd_debit: string; ytd_credit: string }
export interface OpeningCheck { ledger_id: number; debit: string; credit: string; balanced: boolean }

export interface AuxType { id: number; code: string; name: string; is_system: boolean }
export interface AuxItem { id: number; type_id: number; code: string; name: string; enabled: boolean; remark: string }
export interface AuxRef { type_id: number; item_id: number; item_code?: string; item_name?: string }
export interface OpeningAuxRow { aux: AuxRef[]; balance: string; ytd_debit: string; ytd_credit: string }
export interface AuxBalanceRow {
  item_id: number; item_code: string; item_name: string; account_id: number; account_code: string; account_name: string
  open_debit: string; open_credit: string; debit: string; credit: string; end_debit: string; end_credit: string
}

export interface VoucherLine {
  id?: number; ledger_id: number; summary: string; account_id: number
  account_code?: string; account_name?: string; debit: string; credit: string; aux?: AuxRef[]
}
export interface Voucher {
  id: number; year: number; month: number; word: string; number: number; date: string; attachments: number
  status: 'saved' | 'reviewed'; source: string; remark: string; created_by_name: string; reviewed_by_name: string
  total: string; lines: VoucherLine[]; warnings?: string[]
}
export interface ParallelRule {
  id: number; source_ledger_id: number; source_code: string; target_ledger_id: number; target_code: string
  side: '' | 'D' | 'C'; note: string
}

export interface TrialRow {
  account_id: number; code: string; name: string; level: number; is_leaf: boolean
  open_debit: string; open_credit: string; debit: string; credit: string
  ytd_debit: string; ytd_credit: string; end_debit: string; end_credit: string
}
export interface DetailRow {
  kind: 'open' | 'line' | 'month' | 'year'; date?: string; voucher_id?: number; voucher_no?: string
  account?: string; aux?: string; summary: string; debit: string; credit: string; dir: string; balance: string
}
export interface GeneralAccount {
  account_id: number; code: string; name: string
  rows: { period: string; summary: string; debit: string; credit: string; dir: string; balance: string }[]
}

export interface ReportColumn { key: string; name: string }
export interface ReportRow { key?: string; name: string; side?: 'left' | 'right'; indent?: number; bold?: boolean; values?: Record<string, string> }
export interface ReportResult {
  code: string; name: string; layout: 'list' | 'two_side'; period: Period
  columns: ReportColumn[]; rows: ReportRow[]; checks: { name: string; ok: boolean }[]
}
export interface ConfigItem { code: string; name: string; definition: any }

export interface ClosingStatus {
  code: string; name: string; ledger: string; frequency: string; target: string; required: boolean
  applicable: boolean; pending: boolean; amount: string; voucher_ids: number[]
}
export interface CheckItem { name: string; ok: boolean; blocking: boolean; message: string }
