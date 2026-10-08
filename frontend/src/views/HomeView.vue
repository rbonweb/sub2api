<template>
  <!-- Placeholder Home Page: intentionally shows no navigation, login or dashboard links -->
  <div
    data-testid="home-placeholder"
    class="flex min-h-screen items-center justify-center bg-gray-50 px-4 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <h1 class="text-center text-3xl font-semibold tracking-tight md:text-4xl">AI Gateway</h1>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'

// Initialize theme so the placeholder respects the saved or system color scheme
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    document.documentElement.classList.add('dark')
  }
}

onMounted(() => {
  initTheme()
})
</script>
