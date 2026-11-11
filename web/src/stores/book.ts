import { defineStore } from 'pinia'
import { http } from '@/api/http'
import type { Account, AuxItem, AuxType, BookDetail, Ledger } from '@/api/types'

/** 当前账套上下文：基础资料只取一次，各页面共享。 */
export const useBook = defineStore('book', {
  state: () => ({
    detail: null as BookDetail | null,
    accounts: [] as Account[],
    auxTypes: [] as AuxType[],
    auxItems: [] as AuxItem[],
    auxBindings: {} as Record<number, number[]>, // 科目 ID → 启用的辅助核算类别
  }),
  getters: {
    base: (s) => `/books/${s.detail?.id}`,
    ledgers: (s): Ledger[] => s.detail?.ledgers ?? [],
    primaryLedger(): Ledger | undefined {
      return this.ledgers.find((l) => l.is_primary) ?? this.ledgers[0]
    },
    multiLedger(): boolean {
      return this.ledgers.length > 1
    },
    accountById: (s) => {
      const m = new Map<number, Account>()
      s.accounts.forEach((a) => m.set(a.id, a))
      return m
    },
  },
  actions: {
    async load(id: number) {
      if (this.detail?.id !== id) {
        this.detail = null
        this.accounts = []
      }
      const [detail, accounts] = await Promise.all([
        http.get<BookDetail>(`/books/${id}`),
        http.get<Account[]>(`/books/${id}/accounts`),
      ])
      this.detail = detail
      this.accounts = accounts
      await this.reloadAux()
    },
    async reloadAux() {
      ;[this.auxTypes, this.auxItems, this.auxBindings] = await Promise.all([
        http.get<AuxType[]>(`${this.base}/aux/types`),
        http.get<AuxItem[]>(`${this.base}/aux/items`),
        http.get<Record<number, number[]>>(`${this.base}/aux/bindings`),
      ])
    },
    /** 科目启用的辅助核算类别。 */
    auxTypesOf(accountId?: number): AuxType[] {
      const ids = (accountId && this.auxBindings[accountId]) || []
      return ids.map((id) => this.auxTypes.find((t) => t.id === id)!).filter(Boolean)
    },
    auxItemsOf(typeId: number) {
      return this.auxItems.filter((i) => i.type_id === typeId)
    },
    async reloadAccounts() {
      this.accounts = await http.get<Account[]>(`${this.base}/accounts`)
    },
    async reloadDetail() {
      if (this.detail) this.detail = await http.get<BookDetail>(this.base)
    },
    can(perm: string) {
      const p = this.detail?.permissions ?? []
      return p.includes('*') || p.includes(perm)
    },
    ledgerAccounts(ledgerId: number) {
      return this.accounts.filter((a) => a.ledger_id === ledgerId)
    },
  },
})
