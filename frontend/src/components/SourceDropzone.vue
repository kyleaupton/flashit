<script setup lang="ts">
import { computed } from 'vue'
import { Dialogs } from '@wailsio/runtime'
import { useSourceStore } from '@/stores'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { SourceKind } from '@/types'

const sourceStore = useSourceStore()

const hasSource = computed(() => sourceStore.hasSource)
const source = computed(() => sourceStore.source)
const filename = computed(() => sourceStore.filename)
const kindLabel = computed(() => sourceStore.kindLabel)
const fileSize = computed(() => sourceStore.fileSizeFormatted)
const isAnalyzing = computed(() => sourceStore.isAnalyzing)
const error = computed(() => sourceStore.error)
const isUnknown = computed(() => source.value?.kind === SourceKind.Unknown)

const badgeClass = computed(() => {
  switch (source.value?.kind) {
    case SourceKind.LinuxISO:
      return 'badge-linux'
    case SourceKind.WindowsISO:
      return 'badge-windows'
    default:
      return ''
  }
})

async function handleBrowse() {
  const path = await Dialogs.OpenFile({
    Title: 'Select OS Image',
    CanChooseFiles: true,
    CanChooseDirectories: false,
    AllowsOtherFiletypes: true,
    Filters: [
      { DisplayName: 'OS Images', Pattern: '*.iso;*.img' },
      { DisplayName: 'All Files', Pattern: '*.*' },
    ],
  })
  if (path) {
    sourceStore.setSource(path)
  }
}
</script>

<template>
  <!-- Empty State: Dropzone. The Wails runtime marks the card
       file-drop-target-active while a file is dragged over it. -->
  <Card
    v-if="!hasSource"
    class="dropzone-card"
    data-file-drop-target
    @click="handleBrowse"
  >
    <CardContent class="dropzone-content">
      <div class="dropzone-icon">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="48"
          height="48"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <polyline points="17 8 12 3 7 8" />
          <line x1="12" x2="12" y1="3" y2="15" />
        </svg>
      </div>
      <p class="dropzone-text">Drop an ISO here or click to select</p>
      <p class="dropzone-hint">Any hybrid Linux ISO, or a Windows ISO</p>
      <p v-if="error" class="reason">{{ error }}</p>
      <div class="format-chips">
        <span class="format-chip">.iso</span>
        <span class="format-chip">.img</span>
      </div>
    </CardContent>
  </Card>

  <!-- Loaded State: Source Card -->
  <Card v-else class="source-card" data-file-drop-target>
    <CardHeader class="source-header">
      <div class="source-header-row">
        <CardTitle class="source-title">Source</CardTitle>
        <Button variant="ghost" size="sm" @click="sourceStore.clearSource">
          Clear
        </Button>
      </div>
    </CardHeader>
    <CardContent class="source-content">
      <div v-if="isAnalyzing" class="analyzing">
        <div class="spinner" />
        <span>Reading image...</span>
      </div>
      <div v-else-if="source" class="source-info">
        <div class="source-file">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            class="file-icon"
          >
            <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" />
            <path d="M14 2v4a2 2 0 0 0 2 2h4" />
          </svg>
          <span class="filename">{{ filename }}</span>
        </div>
        <div class="source-meta">
          <Badge v-if="kindLabel" variant="secondary" :class="badgeClass">
            {{ kindLabel }}
          </Badge>
          <Badge v-else variant="outline">Unknown image</Badge>
          <span v-if="fileSize" class="file-size">{{ fileSize }}</span>
        </div>
        <p v-if="source.label" class="volume-label">{{ source.label }}</p>
        <p v-if="isUnknown" class="reason">
          Cannot flash this image: {{ source.reason }}.
        </p>
        <p v-if="error" class="reason">{{ error }}</p>
      </div>
    </CardContent>
  </Card>
</template>

<style scoped>
.dropzone-card {
  border: 2px dashed var(--border);
  background: transparent;
  transition: border-color 0.2s, background-color 0.2s;
  cursor: pointer;
}

.dropzone-card:hover,
.dropzone-card.file-drop-target-active {
  border-color: var(--primary);
  background: var(--accent);
}

.dropzone-card.file-drop-target-active {
  border-style: solid;
}

.dropzone-content {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 3rem 2rem;
  text-align: center;
}

.dropzone-icon {
  color: var(--muted-foreground);
  margin-bottom: 1rem;
}

.dropzone-text {
  font-size: 1.125rem;
  font-weight: 500;
  color: var(--foreground);
  margin-bottom: 0.5rem;
}

.dropzone-hint {
  font-size: 0.875rem;
  color: var(--muted-foreground);
}

.format-chips {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.75rem;
}

.format-chip {
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  font-family: monospace;
  background: var(--muted);
  color: var(--muted-foreground);
  border-radius: var(--radius-sm);
}

.source-card {
  overflow: hidden;
  transition: border-color 0.2s;
}

.source-card.file-drop-target-active {
  border-color: var(--primary);
}

.source-header {
  padding-bottom: 0;
}

.source-header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.source-title {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted-foreground);
}

.source-content {
  padding-top: 0.75rem;
}

.analyzing {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  color: var(--muted-foreground);
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--muted);
  border-top-color: var(--primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.source-info {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.source-file {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.file-icon {
  color: var(--muted-foreground);
  flex-shrink: 0;
}

.filename {
  font-weight: 500;
  word-break: break-all;
}

.source-meta {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.file-size {
  font-size: 0.875rem;
  color: var(--muted-foreground);
}

.volume-label {
  margin: 0;
  font-size: 0.875rem;
  font-family: monospace;
  color: var(--muted-foreground);
}

.reason {
  margin: 0;
  font-size: 0.875rem;
  color: var(--destructive);
}

/* OS-specific badge colors */
.badge-linux {
  background: oklch(0.901 0.076 70.697);
  color: oklch(0.47 0.157 37.304);
}

.dark .badge-linux {
  background: oklch(0.47 0.157 37.304 / 20%);
  color: oklch(0.901 0.076 70.697);
}

.badge-windows {
  background: oklch(0.932 0.032 255.585);
  color: oklch(0.488 0.243 264.376);
}

.dark .badge-windows {
  background: oklch(0.488 0.243 264.376 / 20%);
  color: oklch(0.746 0.16 232.661);
}
</style>
