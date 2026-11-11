// 账套内的导航菜单。新增功能页：在此加一项 + 在 router.ts 注册路由。
import type { Component } from 'vue'
import { Tickets, Notebook, DataAnalysis, Finished, Setting } from '@element-plus/icons-vue'

export interface MenuItem {
  title: string
  path: string // 相对 /b/:bookId/
  quick?: string // 出现在首页“快捷入口”，值为一句说明
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
      { title: '新增凭证', path: 'vouchers/new', perm: 'voucher:write', quick: '录入记账凭证' },
      { title: '凭证列表', path: 'vouchers', quick: '查询、审核、整理凭证' },
    ],
  },
  {
    title: '账簿',
    icon: Notebook,
    items: [
      { title: '科目余额表', path: 'ledger/trial', quick: '各科目期初、发生额与余额' },
      { title: '明细账', path: 'ledger/detail', quick: '逐笔查看科目流水' },
      { title: '总账', path: 'ledger/general' },
      { title: '辅助核算余额表', path: 'ledger/aux-balance', quick: '按客户、部门、资金性质等查看' },
      { title: '辅助核算明细账', path: 'ledger/aux-detail' },
    ],
  },
  { title: '报表', icon: DataAnalysis, items: [], dynamic: 'reports' },
  { title: '期末', icon: Finished, items: [{ title: '结转与结账', path: 'closing', quick: '期末结转、结账检查' }] },
  {
    title: '设置',
    icon: Setting,
    items: [
      { title: '会计科目', path: 'settings/accounts' },
      { title: '辅助核算', path: 'settings/aux' },
      { title: '期初余额', path: 'settings/opening' },
      { title: '平行记账规则', path: 'settings/parallel', multiLedgerOnly: true },
      { title: '账套设置', path: 'settings/book', perm: 'book:admin' },
    ],
  },
]
