<script setup lang="ts">
import { computed } from 'vue'
import { Browser } from '@wailsio/runtime'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import ProgressBar from '@/components/ProgressBar.vue'
import { useJobStore, useUpdateStore } from '@/stores'
import { formatSize } from '@/lib/utils'

// Release notes are GitHub text: long ones are cut rather than rendered whole.
const maxNotes = 20_000

const update = useUpdateStore()
const job = useJobStore()

const notesHTML = computed(() => {
  const notes = update.release?.notes?.slice(0, maxNotes) ?? ''
  if (!notes.trim()) return ''
  const html = marked.parse(notes, { async: false, gfm: true }) as string
  return DOMPurify.sanitize(html, { FORBID_TAGS: ['img', 'style', 'form', 'input'], FORBID_ATTR: ['style'] })
})

const published = computed(() => {
  const at = update.release?.publishedAt
  if (!at || at.startsWith('0001')) return ''
  return new Date(at).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' })
})

const busy = computed(() => ['downloading', 'verifying', 'installing'].includes(update.phase))
const progressLine = computed(() => {
  if (update.phase === 'verifying') return 'Verifying…'
  if (update.phase === 'installing') return 'Unpacking…'
  if (!update.total) return 'Starting download…'
  return `${formatSize(update.written)} of ${formatSize(update.total)}`
})

// The first focusable element would be a link in the notes, drawn with a
// focus ring; the primary button is the better start.
function focusPrimary(e: Event) {
  e.preventDefault()
  const content = (e.target as HTMLElement | null) ?? document
  const button = content.querySelector<HTMLButtonElement>('[data-autofocus]:not(:disabled)')
  ;(button ?? (e.target as HTMLElement | null))?.focus()
}

// Links in the notes open in the browser, never in the webview.
function onNotesClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  e.preventDefault()
  const href = a.getAttribute('href') ?? ''
  if (/^https?:\/\//i.test(href)) Browser.OpenURL(href)
}
</script>

<template>
  <Dialog v-model:open="update.sheetOpen">
    <DialogContent
      class="flex max-h-[340px] w-[480px] max-w-[calc(100%-2rem)] flex-col gap-3 p-4 outline-none"
      :show-close-button="!busy"
      @open-auto-focus="focusPrimary"
    >
      <template v-if="update.phase === 'error'">
        <DialogTitle class="text-[14px] font-semibold">The update didn't work</DialogTitle>
        <DialogDescription class="selectable line-clamp-4 text-[12px] break-words text-muted-foreground">
          {{ update.error }}
        </DialogDescription>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-default" @click="update.sheetOpen = false">Later</button>
          <button type="button" class="btn btn-primary" data-autofocus @click="update.retry()">Try again</button>
        </div>
      </template>

      <template v-else-if="update.phase === 'ready'">
        <DialogTitle class="truncate pr-6 text-[14px] font-semibold">FlashIt {{ update.release?.version }} is ready</DialogTitle>
        <DialogDescription class="text-[12px] text-muted-foreground">
          {{ job.isActive ? 'Restart once the drive is finished: restarting now would stop the flash.' : 'Restart FlashIt to start using it.' }}
        </DialogDescription>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-default" @click="update.sheetOpen = false">Later</button>
          <button type="button" class="btn btn-primary" data-autofocus :disabled="job.isActive" @click="update.restart()">Restart now</button>
        </div>
      </template>

      <template v-else>
        <div>
          <DialogTitle class="truncate pr-6 text-[14px] font-semibold">FlashIt {{ update.release?.version }} is available</DialogTitle>
          <DialogDescription class="truncate text-[12px] text-muted-foreground">
            You have {{ update.version }}.<template v-if="published"> Released {{ published }}.</template>
          </DialogDescription>
        </div>
        <div
          @click="onNotesClick"
          class="notes selectable min-h-0 flex-1 overflow-y-auto rounded-md border bg-card px-3 py-2 text-[12.5px]"
        >
          <div v-if="notesHTML" v-html="notesHTML" />
          <div v-else class="text-muted-foreground">No release notes.</div>
        </div>
        <div v-if="busy" class="flex flex-col gap-1.5">
          <ProgressBar :value="update.percent" :indeterminate="update.phase !== 'downloading' || !update.total" />
          <span class="text-[12px] text-muted-foreground tabular-nums">{{ progressLine }}</span>
        </div>
        <div v-else class="flex justify-end gap-2">
          <button type="button" class="btn btn-default" @click="update.sheetOpen = false">Later</button>
          <button type="button" class="btn btn-primary" data-autofocus @click="update.install()">Download and install</button>
        </div>
      </template>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
.notes :deep(h1),
.notes :deep(h2),
.notes :deep(h3) {
  font-weight: 600;
  margin: 0.6em 0 0.3em;
}
.notes :deep(p) {
  margin: 0.4em 0;
}
.notes :deep(ul) {
  list-style: disc;
  padding-left: 1.2em;
}
.notes :deep(ol) {
  list-style: decimal;
  padding-left: 1.2em;
}
.notes :deep(a) {
  color: var(--primary);
  text-decoration: underline;
  cursor: pointer;
}
.notes :deep(code) {
  font-family: var(--font-mono);
  font-size: 11.5px;
}
</style>
