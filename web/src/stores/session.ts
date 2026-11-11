import { defineStore } from 'pinia'
import { http, tokenStore } from '@/api/http'
import type { User } from '@/api/types'

export const useSession = defineStore('session', {
  state: () => ({ user: null as User | null, token: tokenStore.get() }),
  actions: {
    async login(username: string, password: string) {
      const res = await http.post<{ token: string; user: User }>('/auth/login', { username, password })
      this.token = res.token
      this.user = res.user
      tokenStore.set(res.token)
    },
    async ensureUser() {
      if (!this.user && this.token) this.user = await http.get<User>('/auth/me')
      return this.user
    },
    logout() {
      this.token = ''
      this.user = null
      tokenStore.set('')
    },
  },
})
