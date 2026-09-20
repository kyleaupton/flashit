<script setup lang="ts">
import { ref, computed } from 'vue'
import type { AppState } from '@/types'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

const props = defineProps<{
  status: AppState
  error?: string | null
  tccDenied?: boolean
}>()

const emit = defineEmits<{
  openSettings: []
  retry: []
}>()

const showDetails = ref(false)

const isSuccess = computed(() => props.status === 'complete')
const isCancelled = computed(() => props.status === 'cancelled')
const isTccDenied = computed(() => props.status === 'error' && props.tccDenied)
const isError = computed(() => props.status === 'error' && !props.tccDenied)
</script>

<template>
  <!-- Success Alert -->
  <Alert v-if="isSuccess" class="success-alert">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="12" cy="12" r="10" />
      <path d="m9 12 2 2 4-4" />
    </svg>
    <AlertTitle>Success</AlertTitle>
    <AlertDescription>
      Your bootable USB drive has been created successfully. You can safely remove it now.
    </AlertDescription>
  </Alert>

  <!-- Cancelled Alert -->
  <Alert v-else-if="isCancelled" class="cancelled-alert">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="12" cy="12" r="10" />
      <path d="m15 9-6 6" />
      <path d="m9 9 6 6" />
    </svg>
    <AlertTitle>Cancelled</AlertTitle>
    <AlertDescription>
      The operation was cancelled. Your drive may be in an incomplete state.
    </AlertDescription>
  </Alert>

  <!-- Removable Volumes was refused; nothing has touched the drive -->
  <Alert v-else-if="isTccDenied" class="approval-alert">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <rect width="18" height="11" x="3" y="11" rx="2" ry="2" />
      <path d="M7 11V7a5 5 0 0 1 10 0v4" />
    </svg>
    <AlertTitle>Permission needed</AlertTitle>
    <AlertDescription>
      <span>
        FlashIt needs access to removable volumes to write your drive. Allow
        FlashIt under Privacy &amp; Security &rsaquo; Files and Folders &rsaquo;
        Removable Volumes, then try again.
      </span>
      <div class="approval-actions">
        <Button size="sm" @click="emit('openSettings')">Open System Settings</Button>
        <Button size="sm" variant="secondary" @click="emit('retry')">Try again</Button>
      </div>
    </AlertDescription>
  </Alert>

  <!-- Error Alert -->
  <Alert v-else-if="isError" variant="destructive" class="error-alert">
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <circle cx="12" cy="12" r="10" />
      <line x1="12" x2="12" y1="8" y2="12" />
      <line x1="12" x2="12.01" y1="16" y2="16" />
    </svg>
    <AlertTitle>Error</AlertTitle>
    <AlertDescription>
      <span>An error occurred while creating the bootable drive.</span>
      <button
        v-if="error"
        class="details-toggle"
        @click="showDetails = !showDetails"
      >
        {{ showDetails ? 'Hide details' : 'Show details' }}
      </button>
      <pre v-if="showDetails && error" class="error-details">{{ error }}</pre>
    </AlertDescription>
  </Alert>
</template>

<style scoped>
.success-alert {
  border-color: var(--chart-2);
  background: color-mix(in oklch, var(--chart-2) 10%, var(--card));
}

.success-alert svg {
  color: var(--chart-2);
}

.cancelled-alert {
  border-color: var(--muted-foreground);
  background: color-mix(in oklch, var(--muted-foreground) 10%, var(--card));
}

.cancelled-alert svg {
  color: var(--muted-foreground);
}

.error-alert {
  border-color: var(--destructive);
}

.approval-alert {
  border-color: var(--primary);
  background: color-mix(in oklch, var(--primary) 10%, var(--card));
}

.approval-alert svg {
  color: var(--primary);
}

.approval-actions {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.75rem;
}

.details-toggle {
  display: block;
  margin-top: 0.5rem;
  padding: 0;
  font-size: 0.75rem;
  color: inherit;
  opacity: 0.8;
  background: none;
  border: none;
  cursor: pointer;
  text-decoration: underline;
}

.details-toggle:hover {
  opacity: 1;
}

.error-details {
  margin-top: 0.5rem;
  padding: 0.5rem;
  font-size: 0.75rem;
  font-family: monospace;
  background: var(--muted);
  border-radius: var(--radius);
  white-space: pre-wrap;
  word-break: break-word;
  overflow-x: auto;
  max-height: 150px;
  overflow-y: auto;
}
</style>
