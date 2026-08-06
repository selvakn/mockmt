import axios from 'axios'

const api = axios.create({
  baseURL: '/',
  timeout: 10000
})

// Add token to requests
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Handle auth errors
api.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('token')
      window.location.href = '/login'
    }
    return Promise.reject(error)
  }
)

// getRelayStatus reports whether this instance is in relay-with-approval
// mode and whether the current user may act as a reviewer. Never returns
// upstream host, port, or credentials (FR-006) -- callable by any
// authenticated user, not just reviewers, since the portal needs it to
// decide what to render.
export async function getRelayStatus() {
  const response = await api.get('/api/relay/status')
  return response.data
}

// getRelayQueue lists queued messages, optionally filtered by state
// (FR-019) and paginated (SC-008).
export async function getRelayQueue({ state = 'pending_review', limit = 50, offset = 0 } = {}) {
  const response = await api.get('/api/relay/queue', { params: { state, limit, offset } })
  return response.data
}

// getRelayMessage fetches one queued message's full detail for review
// (FR-016), including both the envelope and header sender identities.
export async function getRelayMessage(id) {
  const response = await api.get(`/api/relay/messages/${id}`)
  return response.data
}

// sendRelayMessage approves and relays a pending (or, with
// confirmDuplicateRisk, a failed-indeterminate) message (FR-020). The
// request resolves with the outcome regardless of whether delivery
// succeeded -- a failed relay is still a normal response, not a thrown
// error (contracts/relay-api.md).
export async function sendRelayMessage(id, { confirmDuplicateRisk = false } = {}) {
  const response = await api.post(`/api/relay/messages/${id}/send`, {
    confirm_duplicate_risk: confirmDuplicateRisk
  })
  return response.data
}

// rejectRelayMessage rejects a pending message, or abandons a failed one
// (FR-026, FR-026a).
export async function rejectRelayMessage(id, reason = '') {
  const response = await api.post(`/api/relay/messages/${id}/reject`, { reason })
  return response.data
}

// getRelayAudit fetches the full state-change history and delivery
// attempts for a message (FR-030), available even after its content has
// been purged (FR-031).
export async function getRelayAudit(id) {
  const response = await api.get(`/api/relay/messages/${id}/audit`)
  return response.data
}

// fetchAsObjectUrl is the *only* sanctioned way to load message content
// (attachments, and anything else served under /api/relay/messages) into
// the browser. The portal authenticates with a bearer header, which the
// browser will not attach to a plain <img src> or <iframe src>
// subresource load -- setting src directly to an API path 401s and looks
// like a broken preview rather than an auth bug (research R17). Callers
// must URL.revokeObjectURL the result when the preview closes or the
// message changes, or blobs accumulate for the lifetime of the document.
export async function fetchAsObjectUrl(path) {
  const response = await api.get(path, { responseType: 'blob' })
  return URL.createObjectURL(response.data)
}

// relayAttachmentPath builds the path fetchAsObjectUrl (or a download
// link) should use for one attachment.
export function relayAttachmentPath(messageId, index, { inline = false } = {}) {
  const disposition = inline ? '?disposition=inline' : ''
  return `/api/relay/messages/${messageId}/attachments/${index}${disposition}`
}

export default api