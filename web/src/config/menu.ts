// 账套内的导航菜单。新增功能页：在此加一项 + 在 router.ts 注册路由。
import type { Component } from 'vue'
import { Tickets, Notebook, DataAnalysis, Finished, Setting } from '@element-plus/icons-vue'

export interface MenuItem {
  title: string
  path: string // 相对 /b/:bookId/
  perm?: string // 需要的权限点
  multiLedgerOnly?: boolean // 仅平行记账（多账簿）账套显示
}
export interface MenuGroup {
  title: string
  icon: Component
  items: MenuItem[]
  dynamic?: 'reports' // 子项由账套的报表模板动态生成
}

export const menu: MenuGroup[] = [
  {
    title: '凭证',
    icon: Tickets,
    items: [
      { title: '新增凭证', path: 'vouchers/new', perm: 'voucher:write' },
      { title: '凭证列表', path: 'vouchers' },
    ],
  },
  {
    title: '账簿',
    icon: Notebook,
    items: [
      { title: '科目余额表', path: 'ledger/trial' },
      { title: '明细账', path: 'ledger/detail' },
      { title: '总账', path: 'ledger/general' },
    ],
  },
  { title: '报表', icon: DataAnalysis, items: [], dynamic: 'reports' },
  { title: '期末', icon: Finished, items: [{ title: '结转与结账', path: 'closing' }] },
  {
    title: '设置',
    icon: Setting,
    items: [
      { title: '会计科目', path: 'settings/accounts' },
      { title: '期初余额', path: 'settings/opening' },
      { title: '平行记账规则', path: 'settings/parallel', multiLedgerOnly: true },
      { title: '账套设置', path: 'settings/book', perm: 'book:admin' },
    ],
  },
]
