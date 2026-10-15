<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string, string> = {
  grid: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z',
  briefcase: 'M9 7V4h6v3M3 7h18v14H3zM3 12l9 3 9-3M12 13v4',
  bookmark: 'M6 3h12v19l-6-4-6 4z', search: 'M21 21l-5-5M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15',
  arrow: 'M5 12h14m-6-6 6 6-6 6', external: 'M14 3h7v7M21 3 10 14M10 3H3v18h18v-7',
  location: 'M20 10c0 6-8 12-8 12S4 16 4 10a8 8 0 1 1 16 0zM12 7a3 3 0 1 0 0 6 3 3 0 0 0 0-6',
  clock: 'M12 8v5l3 2M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20',
  shield: 'M12 2 3 6v6c0 5 9 10 9 10s9-5 9-10V6zM8 12l3 3 5-6',
  list: 'M8 5h13M8 12h13M8 19h13M3 5h1M3 12h1M3 19h1',
  table: 'M3 4h18v16H3zM3 9h18M9 4v16', check: 'M5 12l4 4L19 6', close: 'M6 6l12 12M18 6 6 18',
  filter: 'M3 6h18M6 12h12M9 18h6', logout: 'M9 3H3v18h6M14 7l5 5-5 5M8 12h12',
  chevron: 'm9 5 7 7-7 7', plus: 'M12 5v14M5 12h14', user: 'M20 21v-3c0-3-3-5-8-5s-8 2-8 5v3M12 2a4 4 0 1 0 0 8 4 4 0 0 0 0-8',
}
</script>
<template><svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] ?? paths.briefcase" /></svg></template>
