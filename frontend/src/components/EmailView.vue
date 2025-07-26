<template>
  <div class="h-full flex flex-col">
    <!-- Loading State -->
    <div v-if="loading" class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="flex items-center justify-center h-64">
      <div class="text-red-600">{{ error }}</div>
    </div>

    <!-- Email Content -->
    <div v-else-if="email" class="h-full flex flex-col">
      <!-- Header -->
      <div class="border-b border-gray-200 p-4">
        <div class="flex items-center justify-between mb-4">
          <button
            @click="$emit('back')"
            class="flex items-center text-gray-600 hover:text-gray-900 transition-colors duration-150"
          >
            <svg class="h-5 w-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 19l-7-7m0 0l7-7m-7 7h18" />
            </svg>
            Back to Inbox
          </button>
          <button
            @click="handleDelete"
            class="flex items-center text-red-600 hover:text-red-700 transition-colors duration-150"
          >
            <svg class="h-5 w-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
            </svg>
            Delete
          </button>
        </div>
        
        <h1 class="text-2xl font-bold text-gray-900 mb-2">{{ email.subject }}</h1>
        
        <div class="flex items-center space-x-4 text-sm text-gray-600">
          <div class="flex items-center">
            <svg class="h-4 w-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" />
            </svg>
            <span class="font-medium">From:</span>
            <span class="ml-1">{{ email.from_email }}</span>
          </div>
          <div class="flex items-center">
            <svg class="h-4 w-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ formatDate(email.received_at) }}</span>
          </div>
        </div>
      </div>

      <!-- View Mode Toggle -->
      <div v-if="email.html_body" class="border-b border-gray-200 px-4 py-2">
        <div class="flex space-x-2">
          <button
            @click="viewMode = 'html'"
            :class="[
              'px-3 py-1 text-sm rounded-md transition-colors duration-150',
              viewMode === 'html'
                ? 'bg-primary-100 text-primary-700'
                : 'text-gray-600 hover:text-gray-900'
            ]"
          >
            HTML
          </button>
          <button
            @click="viewMode = 'text'"
            :class="[
              'px-3 py-1 text-sm rounded-md transition-colors duration-150',
              viewMode === 'text'
                ? 'bg-primary-100 text-primary-700'
                : 'text-gray-600 hover:text-gray-900'
            ]"
          >
            Plain Text
          </button>
        </div>
      </div>

      <!-- Email Content -->
      <div class="flex-1 overflow-auto p-4">
        <div v-if="viewMode === 'html' && email.html_body" 
             class="prose max-w-none"
             v-html="email.html_body">
        </div>
        <div v-else class="prose max-w-none">
          <pre class="whitespace-pre-wrap font-sans text-gray-900">{{ email.body }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { ref, onMounted, watch } from 'vue'
import api from '../services/api'

export default {
  name: 'EmailView',
  props: {
    emailId: {
      type: Number,
      required: true
    }
  },
  emits: ['back', 'delete'],
  setup(props, { emit }) {
    const email = ref(null)
    const loading = ref(true)
    const error = ref(null)
    const viewMode = ref('html')

    const fetchEmail = async () => {
      try {
        loading.value = true
        const response = await api.get(`/api/emails/${props.emailId}`)
        email.value = response.data
        error.value = null
      } catch (err) {
        error.value = 'Failed to load email'
      } finally {
        loading.value = false
      }
    }

    const handleDelete = async () => {
      if (!email.value) return
      
      try {
        await api.delete(`/api/emails/${email.value.id}`)
        emit('delete')
      } catch (err) {
        error.value = 'Failed to delete email'
      }
    }

    const formatDate = (dateString) => {
      const date = new Date(dateString)
      return date.toLocaleString()
    }

    onMounted(() => {
      fetchEmail()
    })

    watch(() => props.emailId, () => {
      if (props.emailId) {
        fetchEmail()
      }
    })

    return {
      email,
      loading,
      error,
      viewMode,
      handleDelete,
      formatDate
    }
  }
}
</script> 