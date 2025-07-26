<template>
  <header class="bg-white shadow-sm border-b border-gray-200">
    <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
      <div class="flex justify-between items-center h-16">
        <!-- Logo and Title -->
        <div class="flex items-center">
          <div class="flex-shrink-0">
            <svg class="h-8 w-8 text-primary-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 4.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
            </svg>
          </div>
          <div class="ml-3">
            <h1 class="text-xl font-semibold text-gray-900">WebMail</h1>
            <p class="text-sm text-gray-500">{{ user?.email }}</p>
          </div>
        </div>

        <!-- Stats -->
        <div class="hidden md:flex items-center space-x-6">
          <div v-if="stats" class="flex items-center space-x-4 text-sm text-gray-600">
            <div class="flex items-center">
              <svg class="h-4 w-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 4.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
              </svg>
              <span>{{ stats.total_emails }} emails</span>
            </div>
          </div>
        </div>

        <!-- User Menu -->
        <div class="relative">
          <button
            @click="showDropdown = !showDropdown"
            class="flex items-center space-x-3 text-sm rounded-full focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-primary-500"
          >
            <img
              v-if="user?.picture"
              :src="user.picture"
              :alt="user.name"
              class="h-8 w-8 rounded-full"
            />
            <div v-else class="h-8 w-8 rounded-full bg-gray-300 flex items-center justify-center">
              <svg class="h-5 w-5 text-gray-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" />
              </svg>
            </div>
            <span class="hidden md:block text-gray-700">{{ user?.name }}</span>
          </button>

          <div v-if="showDropdown" class="absolute right-0 mt-2 w-48 bg-white rounded-md shadow-lg py-1 z-50 border border-gray-200">
            <div class="px-4 py-2 text-sm text-gray-700 border-b border-gray-100">
              <div class="font-medium">{{ user?.name }}</div>
              <div class="text-gray-500">{{ user?.email }}</div>
            </div>
            <button
              @click="handleLogout"
              class="w-full text-left px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 flex items-center"
            >
              <svg class="h-4 w-4 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
              </svg>
              Sign out
            </button>
          </div>
        </div>
      </div>
    </div>
  </header>
</template>

<script>
import { ref, onMounted, computed } from 'vue'
import { useAuthStore } from '../stores/auth'
import api from '../services/api'

export default {
  name: 'Header',
  setup() {
    const authStore = useAuthStore()
    const showDropdown = ref(false)
    const stats = ref(null)

    const user = computed(() => authStore.user)

    const handleLogout = () => {
      authStore.logout()
      showDropdown.value = false
    }

    const fetchStats = async () => {
      try {
        const response = await api.get('/api/stats')
        stats.value = response.data
      } catch (error) {
        console.error('Failed to fetch stats:', error)
      }
    }

    onMounted(() => {
      fetchStats()
    })

    return {
      user,
      stats,
      showDropdown,
      handleLogout
    }
  }
}
</script> 