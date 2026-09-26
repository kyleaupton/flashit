<script setup lang="ts">
defineProps<{ value?: number; indeterminate?: boolean }>()
</script>

<template>
  <div
    class="relative h-1.5 overflow-hidden rounded-full bg-track"
    role="progressbar"
    :aria-valuenow="indeterminate ? undefined : Math.round(value ?? 0)"
    aria-valuemin="0"
    aria-valuemax="100"
  >
    <div v-if="indeterminate" class="sweep absolute inset-y-0 w-1/4 rounded-full bg-primary opacity-60" />
    <div
      v-else
      class="absolute inset-y-0 left-0 rounded-full bg-primary transition-[width] duration-300 ease-out"
      :style="{ width: `${Math.min(100, Math.max(0, value ?? 0))}%` }"
    />
  </div>
</template>

<style scoped>
.sweep {
  animation: sweep 1.4s ease-in-out infinite;
}
@keyframes sweep {
  from {
    left: -25%;
  }
  to {
    left: 100%;
  }
}
@media (prefers-reduced-motion: reduce) {
  .sweep {
    animation: none;
    left: 37.5%;
  }
}
</style>
