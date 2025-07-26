<template>
  <div class="flex-1 overflow-hidden">
    <!-- Loading State -->
    <div v-if="loading" class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="flex items-center justify-center h-64">
      <div class="text-red-600">{{ error }}</div>
    </div>

    <!-- Empty State -->
    <div v-else-if="emails.length === 0" class="flex flex-col items-center justify-center h-64 text-gray-500">
      <svg class="h-12 w-12 mb-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 4.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
      </svg>
      <p>No emails yet</p>
      <p class="text-sm">Send an email to your address to get started</p>
    </div>

    <!-- Email List -->
    <div v-else class="divide-y divide-gray-200">
      <div
        v-for="email in emails"
        :key="email.id"
        @click="$emit('email-select', email)"
        :class="[
          'p-4 hover:bg-gray-50 cursor-pointer transition-colors duration-150',
          selectedEmailId === email.id ? 'bg-primary-50 border-r-2 border-primary-600' : ''
        ]"
      >
        <div class="flex items-start justify-between">
          <div class="flex-1 min-w-0">
            <div class="flex items-center space-x-3">
              <div class="flex-shrink-0">
                <svg 
                  :class="[
                    'h-5 w-5',
                    selectedEmailId === email.id ? 'text-primary-600' : 'text-gray-400'
                  ]"
                  fill="none" 
                  stroke="currentColor" 
                  viewBox="0 0 24 24"
                >
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 4.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
                </svg>
              </div>
              <div class="flex-1 min-w-0">
                <p class="text-sm font-medium text-gray-900 truncate">
                  {{ email.from_email }}
                </p>
                <p class="text-sm text-gray-900 font-semibold truncate">
                  {{ email.subject }}
                </p>
                <p class="text-sm text-gray-500 truncate">
                  {{ truncateText(email.body, 100) }}
                </p>
              </div>
            </div>
          </div>
          <div class="flex items-center space-x-2">
            <span class="text-xs text-gray-400">
              {{ formatDate(email.received_at) }}
            </span>
            <button
              @click.stop="handleDelete(email.id)"
              class="text-gray-400 hover:text-red-600 transition-colors duration-150"
              title="Delete email"
            >
              <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, onMounted } from 'vue'
import api from '../services/api'

export default {
  name: 'EmailList',
  props: {
    selectedEmailId: {
      type: Number,
      default: null
    }
  },
  emits: ['email-select'],
  setup() {
    const emails = ref([])
    const loading = ref(true)
    const error = ref(null)

    const fetchEmails = async () => {
      try {
        loading.value = true
        const response = await api.get('/api/emails')
        emails.value = response.data
        error.value = null
      } catch (err) {
        error.value = 'Failed to load emails'
      } finally {
        loading.value = false
      }
    }

    const handleDelete = async (emailId) => {
      try {
        await api.delete(`/api/emails/${emailId}`)
        emails.value = emails.value.filter(email => email.id !== emailId)
      } catch (err) {
        error.value = 'Failed to delete email'
      }
    }

    const formatDate = (dateString) => {
      const date = new Date(dateString)
      const now = new Date()
      const diffInHours = (now.getTime() - date.getTime()) / (1000 * 60 * 60)
      
      if (diffInHours < 24) {
        return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
      } else if (diffInHours < 168) {
        return date.toLocaleDateString([], { weekday: 'short' })
      } else {
        return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
      }
    }

    const truncateText = (text, maxLength = 100) => {
      if (text.length <= maxLength) return text
      return text.substring(0, maxLength) + '...'
    }

    onMounted(() => {
      fetchEmails()
    })

    return {
      emails,
      loading,
      error,
      handleDelete,
      formatDate,
      truncateText
    }
  }
}
</script> 