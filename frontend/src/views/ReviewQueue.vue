<template>
  <div class="h-screen flex flex-col bg-gray-50">
    <Header />

    <div class="flex-1 flex overflow-hidden">
      <!-- Queue list -->
      <div :class="[
        'border-r border-gray-200 bg-white flex flex-col transition-all duration-300',
        selectedMessageId ? 'w-1/3' : 'w-full'
      ]">
        <div class="p-4 border-b border-gray-200 flex items-center justify-between">
          <h2 class="text-lg font-semibold text-gray-900">Review Queue</h2>
          <select v-model="stateFilter" class="text-sm border border-gray-300 rounded px-2 py-1">
            <option value="pending_review">Pending Review</option>
            <option value="sending">Sending</option>
            <option value="sent">Sent</option>
            <option value="failed">Failed</option>
            <option value="rejected">Rejected</option>
            <option value="all">All</option>
          </select>
        </div>

        <div v-if="loading" class="flex items-center justify-center h-64">
          <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
        </div>

        <div v-else-if="error" class="flex items-center justify-center h-64">
          <div class="text-red-600">{{ error }}</div>
        </div>

        <div v-else-if="messages.length === 0" class="flex flex-col items-center justify-center h-64 text-gray-500">
          <p>No messages in this state</p>
        </div>

        <div v-else class="flex-1 flex flex-col overflow-hidden">
          <div class="divide-y divide-gray-200 overflow-y-auto flex-1">
            <div
              v-for="msg in messages"
              :key="msg.id"
              @click="selectedMessageId = msg.id"
              :class="[
                'p-4 hover:bg-gray-50 cursor-pointer transition-colors duration-150',
                selectedMessageId === msg.id ? 'bg-primary-50 border-r-2 border-primary-600' : ''
              ]"
            >
              <div class="flex items-start justify-between">
                <div class="flex-1 min-w-0">
                  <p class="text-sm font-medium text-gray-900 truncate">{{ msg.envelope_from }}</p>
                  <p class="text-sm text-gray-900 font-semibold truncate">{{ msg.subject }}</p>
                  <p class="text-xs text-gray-500 truncate">
                    To: {{ recipientSummary(msg.recipients) }}
                    <span v-if="hasHiddenRecipient(msg.recipients)" class="text-amber-600 font-medium">(+ hidden)</span>
                  </p>
                </div>
                <div class="flex flex-col items-end space-y-1 ml-2">
                  <span class="text-xs text-gray-400">{{ formatDate(msg.received_at) }}</span>
                  <span :class="['text-xs px-2 py-0.5 rounded-full font-medium', stateBadgeClass(msg.state)]">
                    {{ msg.state }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <!-- Pagination: without this, only the first PAGE_SIZE messages
               are ever reachable -- at SC-008's 500-message queue, that
               leaves 450 permanently invisible. -->
          <div v-if="total > pageSize" class="p-3 border-t border-gray-200 flex items-center justify-between text-sm text-gray-600">
            <span>{{ rangeStart }}&ndash;{{ rangeEnd }} of {{ total }}</span>
            <div class="space-x-2">
              <button
                @click="goToPreviousPage"
                :disabled="offset === 0"
                class="px-3 py-1 border border-gray-300 rounded disabled:opacity-40 disabled:cursor-not-allowed hover:bg-gray-50"
              >
                Previous
              </button>
              <button
                @click="goToNextPage"
                :disabled="rangeEnd >= total"
                class="px-3 py-1 border border-gray-300 rounded disabled:opacity-40 disabled:cursor-not-allowed hover:bg-gray-50"
              >
                Next
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Detail pane -->
      <div v-if="selectedMessageId" class="w-2/3 bg-white">
        <ReviewMessage
          :message-id="selectedMessageId"
          @back="selectedMessageId = null"
          @decided="handleDecided"
        />
      </div>
    </div>
  </div>
</template>

<script>
import { ref, computed, watch, onMounted } from 'vue'
import Header from '../components/Header.vue'
import ReviewMessage from '../components/ReviewMessage.vue'
import { getRelayQueue } from '../services/api'

const PAGE_SIZE = 50

export default {
  name: 'ReviewQueue',
  components: { Header, ReviewMessage },
  setup() {
    const messages = ref([])
    const loading = ref(true)
    const error = ref(null)
    const stateFilter = ref('pending_review')
    const selectedMessageId = ref(null)
    const offset = ref(0)
    const total = ref(0)

    const pageSize = PAGE_SIZE
    const rangeStart = computed(() => (total.value === 0 ? 0 : offset.value + 1))
    const rangeEnd = computed(() => Math.min(offset.value + messages.value.length, total.value))

    const fetchQueue = async () => {
      try {
        loading.value = true
        const data = await getRelayQueue({ state: stateFilter.value, limit: PAGE_SIZE, offset: offset.value })
        messages.value = data.messages
        total.value = data.total
        error.value = null
      } catch (err) {
        error.value = 'Failed to load the review queue'
      } finally {
        loading.value = false
      }
    }

    const goToNextPage = () => {
      if (rangeEnd.value >= total.value) return
      offset.value += PAGE_SIZE
      fetchQueue()
    }

    const goToPreviousPage = () => {
      if (offset.value === 0) return
      offset.value = Math.max(0, offset.value - PAGE_SIZE)
      fetchQueue()
    }

    const handleDecided = () => {
      selectedMessageId.value = null
      fetchQueue()
    }

    const recipientSummary = (recipients) => recipients.map((r) => r.address).join(', ')
    const hasHiddenRecipient = (recipients) => recipients.some((r) => r.hidden)

    const stateBadgeClass = (state) => ({
      pending_review: 'bg-blue-100 text-blue-800',
      sending: 'bg-amber-100 text-amber-800',
      sent: 'bg-green-100 text-green-800',
      failed: 'bg-red-100 text-red-800',
      rejected: 'bg-gray-100 text-gray-800'
    }[state] || 'bg-gray-100 text-gray-800')

    const formatDate = (dateString) => new Date(dateString).toLocaleString()

    watch(stateFilter, () => {
      // Switching filters starts back at the first page -- the old
      // offset almost certainly doesn't line up with the new filter's
      // result set.
      offset.value = 0
      fetchQueue()
    })
    onMounted(fetchQueue)

    return {
      messages,
      loading,
      error,
      stateFilter,
      selectedMessageId,
      offset,
      total,
      pageSize,
      rangeStart,
      rangeEnd,
      recipientSummary,
      hasHiddenRecipient,
      stateBadgeClass,
      formatDate,
      handleDecided,
      goToNextPage,
      goToPreviousPage
    }
  }
}
</script>
