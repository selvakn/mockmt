<template>
  <div class="h-screen flex flex-col bg-gray-50">
    <!-- Header -->
    <Header />
    
    <!-- Main Content -->
    <div class="flex-1 flex overflow-hidden">
      <!-- Email List Sidebar -->
      <div :class="[
        'border-r border-gray-200 bg-white flex flex-col transition-all duration-300',
        selectedEmail ? 'w-1/3' : 'w-full'
      ]">
        <EmailList 
          @email-select="handleEmailSelect"
          :selected-email-id="selectedEmail?.id"
        />
      </div>

      <!-- Email View -->
      <div v-if="selectedEmail" class="w-2/3 bg-white">
        <EmailView
          :email-id="selectedEmail.id"
          @back="handleBackToInbox"
          @delete="handleEmailDelete"
        />
      </div>
    </div>
    <footer class="w-full text-center py-4 text-gray-500 text-xs bg-white border-t border-gray-200">
      MockMT (Email Mock) &mdash; <a href="https://github.com/selvakn/mockmt" target="_blank" class="text-primary-600 hover:underline">GitHub</a><br/>
      Modern SMTP test application for email delivery testing. Built with Go, Vue.js, and Tailwind CSS.
    </footer>
  </div>
</template>

<script>
import { ref, onMounted } from 'vue'
import { useAuthStore } from '../stores/auth'
import Header from '../components/Header.vue'
import EmailList from '../components/EmailList.vue'
import EmailView from '../components/EmailView.vue'

export default {
  name: 'Dashboard',
  components: {
    Header,
    EmailList,
    EmailView
  },
  setup() {
    const authStore = useAuthStore()
    const selectedEmail = ref(null)

    const handleEmailSelect = (email) => {
      selectedEmail.value = email
    }

    const handleBackToInbox = () => {
      selectedEmail.value = null
    }

    const handleEmailDelete = () => {
      selectedEmail.value = null
    }

    onMounted(async () => {
      await authStore.initAuth()
    })

    return {
      selectedEmail,
      handleEmailSelect,
      handleBackToInbox,
      handleEmailDelete
    }
  }
}
</script> 