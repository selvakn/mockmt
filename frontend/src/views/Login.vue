<template>
  <div class="min-h-screen flex items-center justify-center bg-gradient-to-br from-primary-50 to-indigo-100 px-4 sm:px-6 lg:px-8">
    <div class="max-w-md w-full space-y-8">
      <div>
        <div class="mx-auto h-12 w-12 flex items-center justify-center rounded-full bg-primary-100">
          <svg class="h-8 w-8 text-primary-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 8l7.89 4.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
          </svg>
        </div>
        <h2 class="mt-6 text-center text-3xl font-extrabold text-gray-900">
          Welcome to MockMT
        </h2>
        <p class="mt-2 text-center text-sm text-gray-600">
          Modern SMTP test application for email delivery testing
        </p>
      </div>
      
      <div class="mt-8 space-y-6">
        <div>
          <button
            @click="handleOAuthLogin"
            class="group relative w-full flex justify-center py-3 px-4 border border-transparent text-sm font-medium rounded-lg text-white bg-primary-600 hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-primary-500 transition-colors duration-200"
          >
            <svg class="w-5 h-5 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            Sign in with OAuth
          </button>
        </div>
        
                  <div class="text-center">
            <p class="text-sm text-gray-500">
              MockMT (Mock Mail) &mdash; <a href="https://github.com/selvakn/mockmt" target="_blank" class="text-primary-600 hover:underline">GitHub</a><br/>
            </p>
          </div>
      </div>
    </div>
  </div>
</template>

<script>
export default {
  name: 'Login',
  methods: {
    handleOAuthLogin() {
      window.location.href = '/auth/oauth'
    }
  }
}
</script> 