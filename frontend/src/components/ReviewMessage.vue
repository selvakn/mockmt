<template>
  <div class="h-full flex flex-col">
    <div class="p-4 border-b border-gray-200 flex items-start justify-between">
      <div class="min-w-0">
        <button @click="$emit('back')" class="text-sm text-primary-600 hover:underline mb-2">&larr; Back to queue</button>
        <h2 class="text-lg font-semibold text-gray-900 truncate">{{ message?.subject }}</h2>
        <p class="text-sm text-gray-500">
          From: {{ message?.envelope_from }}
          <span v-if="message?.header_from && message.header_from !== message.envelope_from" class="text-gray-400">
            (header From: {{ message.header_from }})
          </span>
        </p>
      </div>
      <span :class="['text-xs px-2 py-0.5 rounded-full font-medium whitespace-nowrap', stateBadgeClass]">
        {{ message?.state }}
      </span>
    </div>

    <div v-if="loading" class="flex items-center justify-center h-64">
      <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-600"></div>
    </div>

    <div v-else-if="error" class="p-4 text-red-600">{{ error }}</div>

    <div v-else class="flex-1 overflow-y-auto p-4 space-y-4">
      <!-- Recipients: the envelope list, with blind-carbon addresses flagged (FR-015a) -->
      <div>
        <h3 class="text-sm font-medium text-gray-700 mb-1">Recipients</h3>
        <ul class="text-sm space-y-1">
          <li v-for="r in message.recipients" :key="r.address" class="flex items-center space-x-2">
            <span>{{ r.address }}</span>
            <span v-if="r.hidden" class="text-xs px-1.5 py-0.5 rounded bg-amber-100 text-amber-800">hidden from other recipients</span>
            <span v-if="r.delivered" class="text-xs px-1.5 py-0.5 rounded bg-green-100 text-green-800">delivered</span>
          </li>
        </ul>
      </div>

      <div v-if="message.purged" class="p-4 bg-gray-100 rounded text-gray-600 text-sm">
        This message's content was purged under the retention policy. Metadata and audit history remain available.
      </div>

      <template v-else>
        <!-- HTML body, rendered in an isolated, script-free, same-origin-free sandbox
             (FR-016c) with a restrictive in-document CSP that blocks remote content
             from loading (FR-016d) -->
        <div v-if="message.html_body">
          <h3 class="text-sm font-medium text-gray-700 mb-1">Message (HTML)</h3>
          <iframe
            sandbox=""
            :srcdoc="sandboxedHtmlDoc"
            class="w-full border border-gray-200 rounded"
            style="height: 300px"
          ></iframe>
        </div>

        <div v-if="message.text_body">
          <h3 class="text-sm font-medium text-gray-700 mb-1">Message (text)</h3>
          <pre class="text-sm whitespace-pre-wrap bg-gray-50 border border-gray-200 rounded p-3">{{ message.text_body }}</pre>
        </div>

        <!-- Attachments: inline preview for images/PDF/plain text, download otherwise
             (FR-016a, FR-016b). Every fetch goes through fetchAsObjectUrl -- never a
             bare <img src> or <a href> pointed at the API, since the bearer header is
             not attached to subresource loads (research R17). -->
        <div v-if="message.attachments && message.attachments.length">
          <h3 class="text-sm font-medium text-gray-700 mb-2">Attachments</h3>
          <div class="space-y-3">
            <div v-for="a in message.attachments" :key="a.index" class="border border-gray-200 rounded p-3">
              <div class="flex items-center justify-between">
                <div class="text-sm">
                  <span class="font-medium">{{ a.filename || '(unnamed)' }}</span>
                  <span class="text-gray-400 ml-2">{{ a.content_type }} &middot; {{ formatSize(a.size_bytes) }}</span>
                </div>
                <button
                  @click="downloadAttachment(a)"
                  class="text-sm text-primary-600 hover:underline"
                >
                  Download
                </button>
              </div>
              <div v-if="a.previewable" class="mt-2">
                <button v-if="!previewUrls[a.index]" @click="previewAttachment(a)" class="text-sm text-primary-600 hover:underline">
                  Preview
                </button>
                <img v-else-if="a.content_type.startsWith('image/')" :src="previewUrls[a.index]" class="max-h-64 border border-gray-200 rounded" />
                <iframe v-else-if="a.content_type === 'application/pdf'" :src="previewUrls[a.index]" class="w-full border border-gray-200 rounded" style="height: 400px"></iframe>
                <pre v-else class="text-sm whitespace-pre-wrap bg-gray-50 border border-gray-200 rounded p-3 max-h-64 overflow-y-auto">{{ previewText[a.index] }}</pre>
              </div>
            </div>
          </div>
        </div>
      </template>

      <div v-if="message.state === 'failed'" class="p-3 bg-red-50 border border-red-200 rounded text-sm text-red-800">
        <strong>{{ message.failure_kind === 'indeterminate' ? 'Possibly delivered' : 'Failed' }}:</strong>
        {{ message.failure_reason }}
      </div>

      <div v-if="actionError" class="p-3 bg-red-50 border border-red-200 rounded text-sm text-red-800">{{ actionError }}</div>

      <!-- History: available even for a purged message, since audit
           records outlive content (FR-031). -->
      <div>
        <button @click="toggleHistory" class="text-sm text-primary-600 hover:underline">
          {{ showHistory ? 'Hide history' : 'Show history' }}
        </button>
        <div v-if="showHistory" class="mt-2 border border-gray-200 rounded divide-y divide-gray-100">
          <div v-if="!audit" class="p-3 text-sm text-gray-500">Loading…</div>
          <template v-else>
            <div v-for="(e, i) in audit.events" :key="'event-' + i" class="p-2 text-sm flex justify-between">
              <span>
                <span v-if="e.from_state">{{ e.from_state }} &rarr; </span>{{ e.to_state }}
                <span v-if="e.detail" class="text-gray-500">({{ e.detail }})</span>
              </span>
              <span class="text-gray-500">{{ e.actor }} &middot; {{ formatDateTime(e.occurred_at) }}</span>
            </div>
            <div v-if="audit.events.length === 0" class="p-3 text-sm text-gray-500">No history yet</div>
          </template>
        </div>
      </div>
    </div>

    <!-- Failed messages: retry (gated behind explicit duplicate-risk
         confirmation when the prior outcome was indeterminate, FR-025a)
         or abandon (FR-026a). -->
    <div v-if="message?.state === 'failed'" class="p-4 border-t border-gray-200 space-y-3">
      <label v-if="message.failure_kind === 'indeterminate'" class="flex items-start space-x-2 text-sm text-amber-800">
        <input type="checkbox" v-model="confirmDuplicateRisk" class="mt-0.5" />
        <span>The last attempt's outcome is unknown -- it may have already been delivered. I understand a retry could send a duplicate.</span>
      </label>
      <div class="flex items-center justify-end space-x-2">
        <button
          @click="handleAbandon"
          :disabled="retrying || rejecting"
          class="px-4 py-2 bg-white border border-gray-300 text-gray-700 text-sm font-medium rounded hover:bg-gray-50 disabled:opacity-50"
        >
          {{ rejecting ? 'Abandoning…' : 'Abandon' }}
        </button>
        <button
          @click="handleRetry"
          :disabled="retrying || rejecting || (message.failure_kind === 'indeterminate' && !confirmDuplicateRisk)"
          class="px-4 py-2 bg-primary-600 text-white text-sm font-medium rounded hover:bg-primary-700 disabled:opacity-50"
        >
          {{ retrying ? 'Retrying…' : 'Retry' }}
        </button>
      </div>
    </div>

    <div v-if="!message?.purged && message?.state === 'pending_review'" class="p-4 border-t border-gray-200 flex items-center justify-end space-x-2">
      <input
        v-model="rejectReason"
        type="text"
        placeholder="Reason (optional)"
        class="text-sm border border-gray-300 rounded px-2 py-2 flex-1 max-w-xs"
      />
      <button
        @click="handleReject"
        :disabled="sending || rejecting"
        class="px-4 py-2 bg-white border border-gray-300 text-gray-700 text-sm font-medium rounded hover:bg-gray-50 disabled:opacity-50"
      >
        {{ rejecting ? 'Rejecting…' : 'Reject' }}
      </button>
      <button
        @click="handleSendNow"
        :disabled="sending || rejecting"
        class="px-4 py-2 bg-primary-600 text-white text-sm font-medium rounded hover:bg-primary-700 disabled:opacity-50"
      >
        {{ sending ? 'Sending…' : 'Send Now' }}
      </button>
    </div>
  </div>
</template>

<script>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { getRelayMessage, sendRelayMessage, rejectRelayMessage, getRelayAudit, fetchAsObjectUrl, relayAttachmentPath } from '../services/api'

export default {
  name: 'ReviewMessage',
  props: {
    messageId: { type: [Number, String], required: true }
  },
  emits: ['back', 'decided'],
  setup(props, { emit }) {
    const message = ref(null)
    const loading = ref(true)
    const error = ref(null)
    const sending = ref(false)
    const rejecting = ref(false)
    const retrying = ref(false)
    const rejectReason = ref('')
    const confirmDuplicateRisk = ref(false)
    const actionError = ref(null)
    const previewUrls = ref({})
    const previewText = ref({})
    const objectUrls = ref([])
    const showHistory = ref(false)
    const audit = ref(null)

    const revokeAllObjectUrls = () => {
      objectUrls.value.forEach((url) => URL.revokeObjectURL(url))
      objectUrls.value = []
      previewUrls.value = {}
      previewText.value = {}
    }

    const fetchMessage = async () => {
      revokeAllObjectUrls()
      showHistory.value = false
      audit.value = null
      try {
        loading.value = true
        message.value = await getRelayMessage(props.messageId)
        error.value = null
      } catch (err) {
        error.value = 'Failed to load the message'
      } finally {
        loading.value = false
      }
    }

    const previewAttachment = async (attachment) => {
      try {
        const path = relayAttachmentPath(props.messageId, attachment.index, { inline: true })
        const url = await fetchAsObjectUrl(path)
        objectUrls.value.push(url)
        if (attachment.content_type.startsWith('text/')) {
          const resp = await fetch(url)
          previewText.value = { ...previewText.value, [attachment.index]: await resp.text() }
        } else {
          previewUrls.value = { ...previewUrls.value, [attachment.index]: url }
        }
      } catch (err) {
        actionError.value = 'Failed to load attachment preview'
      }
    }

    const downloadAttachment = async (attachment) => {
      try {
        const path = relayAttachmentPath(props.messageId, attachment.index)
        const url = await fetchAsObjectUrl(path)
        const link = document.createElement('a')
        link.href = url
        link.download = attachment.filename || 'attachment'
        document.body.appendChild(link)
        link.click()
        document.body.removeChild(link)
        URL.revokeObjectURL(url)
      } catch (err) {
        actionError.value = 'Failed to download attachment'
      }
    }

    const handleSendNow = async () => {
      sending.value = true
      actionError.value = null
      try {
        const result = await sendRelayMessage(props.messageId)
        if (result.state === 'sent') {
          emit('decided')
        } else {
          message.value = { ...message.value, ...result }
        }
      } catch (err) {
        actionError.value = err.response?.data?.error || 'Failed to send the message'
      } finally {
        sending.value = false
      }
    }

    const handleReject = async () => {
      rejecting.value = true
      actionError.value = null
      try {
        await rejectRelayMessage(props.messageId, rejectReason.value)
        emit('decided')
      } catch (err) {
        actionError.value = err.response?.data?.error || 'Failed to reject the message'
      } finally {
        rejecting.value = false
      }
    }

    const handleRetry = async () => {
      retrying.value = true
      actionError.value = null
      try {
        const result = await sendRelayMessage(props.messageId, { confirmDuplicateRisk: confirmDuplicateRisk.value })
        if (result.state === 'sent') {
          emit('decided')
        } else {
          confirmDuplicateRisk.value = false
          message.value = { ...message.value, ...result }
        }
      } catch (err) {
        actionError.value = err.response?.data?.error || 'Failed to retry the message'
      } finally {
        retrying.value = false
      }
    }

    const handleAbandon = async () => {
      rejecting.value = true
      actionError.value = null
      try {
        await rejectRelayMessage(props.messageId, rejectReason.value)
        emit('decided')
      } catch (err) {
        actionError.value = err.response?.data?.error || 'Failed to abandon the message'
      } finally {
        rejecting.value = false
      }
    }

    const sandboxedHtmlDoc = computed(() => {
      const body = message.value?.html_body || ''
      return `<!DOCTYPE html><html><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline' data:; font-src data:;"></head><body>${body}</body></html>`
    })

    const stateBadgeClass = computed(() => ({
      pending_review: 'bg-blue-100 text-blue-800',
      sending: 'bg-amber-100 text-amber-800',
      sent: 'bg-green-100 text-green-800',
      failed: 'bg-red-100 text-red-800',
      rejected: 'bg-gray-100 text-gray-800'
    }[message.value?.state] || 'bg-gray-100 text-gray-800'))

    const toggleHistory = async () => {
      showHistory.value = !showHistory.value
      if (showHistory.value && !audit.value) {
        try {
          audit.value = await getRelayAudit(props.messageId)
        } catch (err) {
          actionError.value = 'Failed to load history'
        }
      }
    }

    const formatDateTime = (dateString) => new Date(dateString).toLocaleString()

    const formatSize = (bytes) => {
      if (bytes < 1024) return `${bytes} B`
      if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
      return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
    }

    watch(() => props.messageId, fetchMessage)
    onMounted(fetchMessage)
    onUnmounted(revokeAllObjectUrls)

    return {
      message,
      loading,
      error,
      sending,
      rejecting,
      retrying,
      rejectReason,
      confirmDuplicateRisk,
      actionError,
      previewUrls,
      previewText,
      showHistory,
      audit,
      sandboxedHtmlDoc,
      stateBadgeClass,
      formatSize,
      formatDateTime,
      previewAttachment,
      downloadAttachment,
      handleSendNow,
      handleReject,
      handleRetry,
      handleAbandon,
      toggleHistory
    }
  }
}
</script>
