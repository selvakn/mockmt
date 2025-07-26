import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../services/api'

export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const token = ref(localStorage.getItem('token'))

  const isAuthenticated = computed(() => !!token.value)

  const login = async (newToken) => {
    token.value = newToken
    localStorage.setItem('token', newToken)
    
    try {
      const response = await api.get('/api/user')
      user.value = response.data
    } catch (error) {
      console.error('Failed to get user info:', error)
      logout()
    }
  }

  const logout = () => {
    user.value = null
    token.value = null
    localStorage.removeItem('token')
  }

  const initAuth = async () => {
    if (token.value) {
      try {
        const response = await api.get('/api/user')
        user.value = response.data
      } catch (error) {
        console.error('Failed to initialize auth:', error)
        logout()
      }
    }
  }

  return {
    user,
    token,
    isAuthenticated,
    login,
    logout,
    initAuth
  }
}) 