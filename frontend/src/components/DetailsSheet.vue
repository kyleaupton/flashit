<script setup lang="ts">
import { computed } from 'vue'
import { Check, Circle, Loader2, X } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { useDrivesStore, useJobStore, useSourceStore } from '@/stores'
import { driveName } from '@/lib/utils'

const open = defineModel<boolean>('open', { required: true })

const job = useJobStore()
const source = useSourceStore()
const drives = useDrivesStore()

const method = computed(() => {
  if (source.isWindows) {
    return source.needsSplit
      ? 'Formats the drive FAT32, copies the Windows files, and splits install.wim into .swm parts.'
      : 'Formats the drive FAT32 and copies the Windows files.'
  }
  return 'Writes the image to the drive as-is, byte for byte.'
})

function time(at: number): string {
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent @open-auto-focus.prevent class="outline-none flex max-h-[340px] w-[520px] max-w-[calc(100%-2rem)] flex-col gap-3 p-4">
      <div>
        <DialogTitle class="text-[14px] font-semibold">Details</DialogTitle>
        <DialogDescription class="line-clamp-3 text-[12px] leading-snug break-words text-muted-foreground">
          {{ source.displayName }} to {{ drives.selectedPick ? driveName(drives.selectedPick) : 'the drive' }}. {{ method }}
        </DialogDescription>
      </div>

      <ol v-if="job.steps.length" class="flex flex-col gap-1">
        <li v-for="step in job.steps" :key="step.key" class="flex min-w-0 items-center gap-2 text-[12.5px]">
          <Check v-if="step.status === 'completed'" class="size-3.5 text-success" />
          <Loader2 v-else-if="step.status === 'running'" class="size-3.5 animate-spin text-primary" />
          <X v-else-if="step.status === 'failed'" class="size-3.5 text-danger" />
          <Circle v-else class="size-3.5 text-muted-foreground" />
          <span class="min-w-0 truncate" :class="step.status === 'pending' && 'text-muted-foreground'">{{ step.name }}</span>
          <span v-if="step.status === 'running' && step.hasProgress" class="ml-auto tabular-nums text-muted-foreground">
            {{ step.progress.toFixed(1) }}%
          </span>
        </li>
      </ol>

      <div class="selectable min-h-0 flex-1 overflow-y-auto rounded-md bg-muted p-2 font-mono text-[11px] leading-relaxed">
        <div v-if="!job.logs.length" class="text-muted-foreground">Nothing logged yet.</div>
        <div
          v-for="(line, i) in job.logs"
          :key="i"
          :class="{ 'text-danger': line.level === 'error', 'text-warn': line.level === 'warning' }"
        >
          <span class="text-muted-foreground">{{ time(line.at) }}</span> <span class="break-words">{{ line.text }}</span>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
