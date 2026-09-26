<script setup lang="ts">
import { computed, ref } from 'vue'
import { CircleCheck, TriangleAlert, Unplug, Usb } from 'lucide-vue-next'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { useAppStore, useDrivesStore, useSourceStore } from '@/stores'
import { basename, formatCapacity, formatSize } from '@/lib/utils'
import type { Drive } from '@/types'

const drives = useDrivesStore()
const source = useSourceStore()
const app = useAppStore()

const open = ref(false)
const locked = computed(() => app.isRunning)
const pick = computed(() => drives.selectedPick)
const list = computed(() => drives.removableDrives)
const imageSize = computed(() => source.source?.size ?? 0)

function driveName(d: Drive): string {
  return d.Model || d.Vendor || 'Unnamed drive'
}

// Volume names from the mountpoints: /Volumes/BACKUP, /media/kyle/BACKUP, E:\.
function volumes(d: Drive): string {
  return (d.Mountpoints ?? []).map((m) => basename(m.replace(/[\\/]+$/, '')) || m).join(', ')
}

function contents(d: Drive): string {
  const v = volumes(d)
  return v ? `Contains ${v}` : 'No mounted volumes'
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
</script>

<template>
  <Popover v-model:open="open">
    <!-- One stable anchor: the slot below swaps elements, and an anchor that
         unmounts leaves the popover positioned against a detached node. It is
         a line under the slot's header, so the list opens over the slot at
         the same place whatever the slot's height. -->
    <div class="relative flex min-h-0 min-w-0">
      <PopoverAnchor class="pointer-events-none absolute inset-x-0 top-[38px] h-0" />
      <div
        v-if="!pick"
        class="slot-empty"
        :class="open && 'border-2 border-solid border-primary'"
      >
        <Usb class="size-7 text-muted-foreground" :stroke-width="1.75" />
        <button type="button" class="btn btn-default" :disabled="locked" @click="open = !open">
          Choose drive…
        </button>
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
            <div class="truncate text-[14px] font-semibold">{{ driveName(pick) }}</div>
            <div class="selectable truncate font-mono text-[12px] text-muted-foreground">{{ detail(pick) }}</div>
          </div>
          <span class="chip shrink-0">{{ formatCapacity(pick.SizeBytes) }}</span>
        </div>

        <div
          v-if="app.state === 'done'"
          class="mt-auto flex items-center gap-2 rounded-md bg-success-bg px-2.5 py-2 text-[12px] text-success"
        >
          <CircleCheck class="size-4 shrink-0" :stroke-width="1.75" />
          <span class="truncate">Now holds {{ source.displayName }}</span>
        </div>
        <div
          v-else-if="drives.isDisconnected && !app.isRunning"
          class="mt-auto flex items-center gap-2 rounded-md bg-muted px-2.5 py-2 text-[12px] text-muted-foreground"
        >
          <Unplug class="size-4 shrink-0" :stroke-width="1.75" />
          <span>Disconnected. Choose it again when it is back.</span>
        </div>
        <div
          v-else-if="!app.fits"
          class="mt-auto flex items-center gap-2 rounded-md bg-danger-bg px-2.5 py-2 text-[12px] text-danger"
        >
          <TriangleAlert class="size-4 shrink-0" :stroke-width="1.75" />
          <span>Too small for this image ({{ formatSize(imageSize) }})</span>
        </div>
        <div
          v-else
          class="mt-auto flex items-center gap-2 rounded-md bg-warn-bg px-2.5 py-2 text-[12px] text-warn"
        >
          <TriangleAlert class="size-4 shrink-0" :stroke-width="1.75" />
          <span v-if="volumes(pick)" class="truncate">Will erase <b>{{ volumes(pick) }}</b></span>
          <span v-else class="truncate">Will erase everything on this drive</span>
        </div>
      </div>
    </div>

    <PopoverContent align="end" side="bottom" :side-offset="0" :collision-padding="8" class="w-[300px] p-1.5" @open-auto-focus.prevent>
      <div class="flex max-h-[226px] flex-col gap-0.5 overflow-y-auto">
        <button
          v-for="d in list"
          :key="d.Device"
          type="button"
          class="flex items-start gap-2.5 rounded-lg px-3 py-2.5 text-left enabled:hover:bg-accent disabled:opacity-50"
          :class="pick?.Device === d.Device && !drives.isDisconnected && 'bg-selected'"
          :disabled="tooSmall(d)"
          @click="choose(d)"
        >
          <span
            class="mt-0.5 size-4 shrink-0 rounded-full border-[1.5px] border-dash"
            :class="pick?.Device === d.Device && !drives.isDisconnected && 'border-[5px] border-primary'"
          />
          <span class="flex min-w-0 flex-1 flex-col gap-0.5">
            <span class="flex items-center justify-between gap-2">
              <span class="truncate text-[13px] font-semibold">{{ driveName(d) }}</span>
              <span class="shrink-0 text-[12px] text-muted-foreground">{{ formatCapacity(d.SizeBytes) }}</span>
            </span>
            <span class="flex items-center gap-1.5">
              <span class="truncate font-mono text-[11.5px] text-muted-foreground">{{ detail(d) }}</span>
              <span
                v-if="drives.isJustConnected(d)"
                class="shrink-0 rounded-sm bg-primary/15 px-1.5 text-[11px] font-medium text-primary"
              >Just connected</span>
            </span>
            <span v-if="tooSmall(d)" class="text-[12px] text-muted-foreground">
              Too small for this image (needs {{ formatSize(imageSize) }})
            </span>
            <span v-else class="truncate text-[12px]" :class="d.Mountpoints?.length ? 'text-warn' : 'text-muted-foreground'">
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
