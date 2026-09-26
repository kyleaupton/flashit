<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { Events } from '@wailsio/runtime'
import { ArrowRight, FileDown } from 'lucide-vue-next'
import { toast } from 'vue-sonner'
import 'vue-sonner/style.css'
import { Toaster } from '@/components/ui/sonner'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import TitleBar from '@/components/TitleBar.vue'
import ImageSlot from '@/components/ImageSlot.vue'
import DriveSlot from '@/components/DriveSlot.vue'
import ActionArea from '@/components/ActionArea.vue'
import DetailsSheet from '@/components/DetailsSheet.vue'
import UpdateSheet from '@/components/UpdateSheet.vue'
import { useAppStore, useDrivesStore, useJobStore, useSourceStore, useUpdateStore } from '@/stores'
import { isMac } from '@/lib/utils'

const app = useAppStore()
const drives = useDrivesStore()
const source = useSourceStore()
const job = useJobStore()
const update = useUpdateStore()

const detailsOpen = ref(false)
const stopOpen = ref(false)

let unsubscribeDrops: (() => void) | null = null

// The backend relays Wails' WindowFilesDropped as files:dropped; the runtime
// reports drops only over a data-file-drop-target element, which the root
// is except during a job.
function onDrop(ev: Events.WailsEvent) {
  if (app.isRunning) return
  const files = (ev.data as { files?: string[] })?.files ?? []
  if (files.length !== 1) {
    toast('Drop one image file')
    return
  }
  app.leaveFinished()
  source.setSource(files[0])
}

function onKey(e: KeyboardEvent) {
  const mod = isMac ? e.metaKey : e.ctrlKey
  if (mod && e.key.toLowerCase() === 'o') {
    e.preventDefault()
    if (!app.isRunning) {
      app.leaveFinished()
      source.choose()
    }
    return
  }
  if (e.key === 'Escape' && app.isRunning && !detailsOpen.value && !update.sheetOpen) {
    e.preventDefault()
    stopOpen.value = true
  }
}

async function stop() {
  stopOpen.value = false
  await job.cancelJob()
}

onMounted(() => {
  drives.startAutoRefresh()
  job.subscribeToEvents()
  update.load()
  unsubscribeDrops = Events.On('files:dropped', onDrop)
  window.addEventListener('keydown', onKey)
})

onUnmounted(() => {
  unsubscribeDrops?.()
  window.removeEventListener('keydown', onKey)
})
</script>

<template>
  <div
    class="app relative flex h-full flex-col overflow-hidden bg-background text-foreground"
    :data-file-drop-target="app.isRunning ? undefined : ''"
  >
    <TitleBar />

    <main class="flex min-h-0 flex-1 flex-col gap-3.5 px-6 pt-5 pb-4">
      <div class="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)_24px_minmax(0,1fr)] gap-2">
        <ImageSlot />
        <div class="flex items-center justify-center text-dash">
          <ArrowRight class="size-5" :stroke-width="1.75" />
        </div>
        <DriveSlot />
      </div>
      <ActionArea @details="detailsOpen = true" />
    </main>

    <div class="drop-overlay pointer-events-none absolute inset-2 hidden flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed border-primary bg-background/90 text-primary">
      <FileDown class="size-8" :stroke-width="1.75" />
      <span class="text-[14px] font-semibold">Drop the image to use it</span>
    </div>

    <DetailsSheet v-model:open="detailsOpen" />
    <UpdateSheet />

    <AlertDialog v-model:open="stopOpen">
      <AlertDialogContent class="w-[380px] gap-3 p-4">
        <AlertDialogTitle class="text-[14px]">Stop flashing?</AlertDialogTitle>
        <AlertDialogDescription class="text-[12.5px]">
          {{ drives.selectedPick?.Model || 'The drive' }} won't be bootable.
        </AlertDialogDescription>
        <AlertDialogFooter class="flex-row justify-end gap-2">
          <AlertDialogCancel class="btn btn-default h-8 shadow-none">Keep flashing</AlertDialogCancel>
          <AlertDialogAction class="btn h-8 border-danger bg-danger text-white hover:bg-danger" @click="stop">Stop</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>

    <Toaster position="bottom-center" />
  </div>
</template>

<style scoped>
.app.file-drop-target-active .drop-overlay {
  display: flex;
}
</style>
