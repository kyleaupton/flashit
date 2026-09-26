<script setup lang="ts">
import { computed, ref } from 'vue'
import { CircleCheck, CircleX, Loader2, TriangleAlert, Unplug, Usb } from 'lucide-vue-next'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { useAppStore, useDrivesStore, useJobStore, useSourceStore } from '@/stores'
import { basename, driveName, formatCapacity, formatSize } from '@/lib/utils'
import type { Drive } from '@/types'

const drives = useDrivesStore()
const source = useSourceStore()
const job = useJobStore()
const app = useAppStore()

const open = ref(false)
const locked = computed(() => app.isRunning)
const pick = computed(() => drives.selectedPick)
const list = computed(() => drives.removableDrives)
const imageSize = computed(() => source.source?.size ?? 0)

// Volume names from the mountpoints: /Volumes/BACKUP, /media/kyle/BACKUP, E:\.
function volumes(d: Drive): string[] {
  return (d.Mountpoints ?? []).map((m) => basename(m.replace(/[\\/]+$/, '')) || m)
}

function contents(d: Drive): string {
  const v = volumes(d)
  return v.length ? `Contains ${v.join(', ')}` : 'No mounted volumes'
}

function tooSmall(d: Drive): boolean {
  return imageSize.value > 0 && d.SizeBytes < imageSize.value
}

function detail(d: Drive): string {
  return [d.Device, d.Protocol].filter(Boolean).join(' · ')
}

function choose(d: Drive) {
  if (tooSmall(d)) return
  app.leaveFinished()
  drives.selectDrive(d)
  open.value = false
}

const countLabel = computed(() => {
  const n = list.value.length
  if (!drives.loaded) return 'Looking for drives…'
  if (n === 0) return 'Insert a USB drive'
  return n === 1 ? '1 removable drive connected' : `${n} removable drives connected`
})

type Tone = 'warn' | 'danger' | 'success' | 'neutral'

// One line at the foot of the slot saying what is about to happen, is
// happening, or happened to the drive.
const status = computed((): { tone: Tone; text: string; strong?: string; after?: string; title?: string } | null => {
  const d = pick.value
  if (!d) return null
  const vols = volumes(d)
  switch (app.state) {
    case 'running':
      return { tone: 'neutral', text: job.isAuthorizing ? 'Waiting for approval' : 'Writing…' }
    case 'done':
      return { tone: 'success', text: `Now holds ${source.displayName}` }
    case 'failed':
    case 'cancelled':
      if (job.driveTouched) {
        return { tone: 'danger', text: app.state === 'failed' ? 'Flash failed, not bootable' : 'Stopped, may not be bootable' }
      }
  }
  if (drives.isDisconnected) return { tone: 'neutral', text: 'Disconnected', title: 'Choose the drive again when it is back.' }
  if (!app.fits) return { tone: 'danger', text: 'Too small for this image', title: `The image is ${formatSize(imageSize.value)}.` }
  if (!vols.length) return { tone: 'warn', text: 'Erases everything on it' }
  return {
    tone: 'warn',
    text: 'Will erase ',
    strong: vols[0],
    after: vols.length > 1 ? ` and ${vols.length - 1} more` : undefined,
    title: `Will erase ${vols.join(', ')}`,
  }
})

const toneClass: Record<Tone, string> = {
  warn: 'bg-warn-bg text-warn',
  danger: 'bg-danger-bg text-danger',
  success: 'bg-success-bg text-success',
  neutral: 'bg-muted text-muted-foreground',
}
</script>

<template>
  <Popover v-model:open="open">
    <!-- One stable anchor: the slot below swaps elements, and an anchor that
         unmounts leaves the popover positioned against a detached node. It is
         a line under the slot's header, so the list opens over the slot at
         the same place whatever the slot's height. -->
    <div class="relative flex min-h-0 min-w-0">
      <PopoverAnchor class="pointer-events-none absolute inset-x-0 top-[38px] h-0" />
      <div v-if="!pick" class="slot-empty" :class="open && 'border-2 border-solid border-primary'">
        <Usb class="size-7 text-muted-foreground" :stroke-width="1.75" />
        <button type="button" class="btn btn-default" :disabled="locked" @click="open = !open">Choose drive…</button>
        <span class="text-[12px] text-muted-foreground">{{ countLabel }}</span>
      </div>

      <div v-else class="slot gap-2.5" :class="drives.isDisconnected && !app.isFinished && 'border-[1.5px] border-dash'">
        <div class="flex h-6 items-center justify-between">
          <span class="text-[12px] font-semibold text-muted-foreground">Drive</span>
          <button v-if="!locked" type="button" class="btn btn-link" @click="open = !open">Change</button>
        </div>
        <div class="flex min-w-0 items-center gap-3">
          <div class="flex size-[38px] shrink-0 items-center justify-center rounded-[8px] bg-muted">
            <Usb class="size-5 text-muted-foreground" :stroke-width="1.75" />
          </div>
          <div class="min-w-0 flex-1">
            <div class="truncate text-[14px] font-semibold" :title="driveName(pick)">{{ driveName(pick) }}</div>
            <div class="truncate text-[12px] text-muted-foreground" :title="`${formatCapacity(pick.SizeBytes)} · ${detail(pick)}`">
              {{ formatCapacity(pick.SizeBytes) }} · <span class="selectable font-mono">{{ detail(pick) }}</span>
            </div>
          </div>
        </div>

        <div
          v-if="status"
          class="mt-auto flex min-w-0 items-center gap-2 rounded-md px-2.5 py-2 text-[12px]"
          :class="toneClass[status.tone]"
          :title="status.title ?? status.text"
        >
          <Loader2 v-if="app.state === 'running'" class="size-4 shrink-0 animate-spin" :stroke-width="1.75" />
          <CircleCheck v-else-if="status.tone === 'success'" class="size-4 shrink-0" :stroke-width="1.75" />
          <Unplug v-else-if="drives.isDisconnected && status.tone === 'neutral'" class="size-4 shrink-0" :stroke-width="1.75" />
          <CircleX v-else-if="status.tone === 'danger' && app.isFinished" class="size-4 shrink-0" :stroke-width="1.75" />
          <TriangleAlert v-else class="size-4 shrink-0" :stroke-width="1.75" />
          <span v-if="!status.strong" class="min-w-0 truncate">{{ status.text }}</span>
          <span v-else class="flex min-w-0 whitespace-pre">
            <span class="shrink-0">{{ status.text }}</span><b class="min-w-0 truncate">{{ status.strong }}</b><span v-if="status.after" class="shrink-0">{{ status.after }}</span>
          </span>
        </div>
      </div>
    </div>

    <PopoverContent
      align="end"
      side="bottom"
      :side-offset="0"
      :collision-padding="8"
      class="w-[300px] p-1.5"
      @open-auto-focus.prevent
    >
      <div class="flex max-h-[226px] flex-col gap-0.5 overflow-y-auto">
        <button
          v-for="d in list"
          :key="d.Device"
          type="button"
          class="flex min-w-0 items-start gap-2.5 rounded-lg px-3 py-2.5 text-left enabled:hover:bg-accent disabled:opacity-50"
          :class="pick?.Device === d.Device && !drives.isDisconnected && 'bg-selected'"
          :disabled="tooSmall(d)"
          @click="choose(d)"
        >
          <span
            class="mt-0.5 size-4 shrink-0 rounded-full border-[1.5px] border-dash"
            :class="pick?.Device === d.Device && !drives.isDisconnected && 'border-[5px] border-primary'"
          />
          <span class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="flex min-w-0 items-center justify-between gap-2">
              <span class="min-w-0 truncate text-[13px] font-semibold" :title="driveName(d)">{{ driveName(d) }}</span>
              <span class="shrink-0 text-[12px] text-muted-foreground">{{ formatCapacity(d.SizeBytes) }}</span>
            </span>
            <span class="flex min-w-0 items-center gap-1.5">
              <span class="min-w-0 truncate font-mono text-[11.5px] text-muted-foreground" :title="detail(d)">{{ detail(d) }}</span>
              <span
                v-if="drives.isJustConnected(d)"
                class="shrink-0 rounded-sm bg-primary/15 px-1.5 text-[11px] font-medium text-primary"
              >Just connected</span>
            </span>
            <span v-if="tooSmall(d)" class="truncate text-[12px] text-muted-foreground">
              Too small for this image (needs {{ formatSize(imageSize) }})
            </span>
            <span
              v-else
              class="truncate text-[12px]"
              :class="d.Mountpoints?.length ? 'text-warn' : 'text-muted-foreground'"
              :title="contents(d)"
            >
              {{ contents(d) }}
            </span>
          </span>
        </button>
        <div v-if="list.length === 0" class="px-3 py-4 text-center text-[12px] text-muted-foreground">
          No removable drives. Insert a USB drive or SD card.
        </div>
      </div>
      <div class="mt-1 border-t px-2.5 pt-2 pb-1 text-[11.5px] leading-snug text-muted-foreground">
        Only removable drives are listed. Internal and system disks never appear here.
      </div>
    </PopoverContent>
  </Popover>
</template>
