<script setup lang="ts">
import { computed } from 'vue'
import { FileIcon, Loader2, TriangleAlert } from 'lucide-vue-next'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useAppStore, useSourceStore } from '@/stores'
import { formatSize, languageName } from '@/lib/utils'
import { SourceKind } from '@/types'

const source = useSourceStore()
const app = useAppStore()

const info = computed(() => source.source)
const locked = computed(() => app.isRunning)

const bootModes = computed(() => {
  const i = info.value
  if (!i) return null
  if (i.bios && i.uefi) return 'UEFI + BIOS'
  if (i.uefi) return 'UEFI'
  if (i.bios) return 'BIOS'
  return null
})

const chips = computed(() => {
  const i = info.value
  if (!i) return []
  const size = i.size ? formatSize(i.size) : null
  if (i.kind === SourceKind.LinuxISO) {
    return ['Linux', i.arch, bootModes.value, size].filter(Boolean) as string[]
  }
  const editions = i.editions?.length ?? 0
  return [
    'Windows',
    i.arch,
    editions > 1 ? `${editions} editions` : null,
    i.language ? languageName(i.language) : null,
    size,
  ].filter(Boolean) as string[]
})

// Middle truncation keeps the extension and the version at the end visible.
const shortFilename = computed(() => {
  const f = source.filename ?? ''
  const max = 25
  if (f.length <= max) return f
  const tail = 14
  return `${f.slice(0, max - tail - 1)}…${f.slice(-tail)}`
})

function choose() {
  app.leaveFinished()
  source.choose()
}

// The name line is the filename itself when the image has no better name.
const hasName = computed(() => !!info.value?.name)
</script>

<template>
  <div v-if="source.status === 'empty'" class="slot-empty">
    <FileIcon class="size-7 text-muted-foreground" :stroke-width="1.75" />
    <button type="button" class="btn btn-default" :disabled="locked" @click="choose">
      Choose image…
    </button>
    <span class="text-[12px] text-muted-foreground">or drop an .iso or .img here</span>
  </div>

  <div
    v-else
    class="slot gap-2.5"
    :class="source.status === 'unusable' && 'border-[1.5px] border-danger'"
  >
    <div class="flex h-6 items-center justify-between">
      <span class="text-[12px] font-semibold text-muted-foreground">Image</span>
      <button v-if="!locked" type="button" class="btn btn-link" @click="choose">Change</button>
    </div>

    <div class="flex min-w-0 items-center gap-3">
      <div class="flex size-[38px] shrink-0 items-center justify-center rounded-[8px] bg-muted">
        <Loader2 v-if="source.status === 'probing'" class="size-5 animate-spin text-muted-foreground" />
        <FileIcon v-else class="size-5 text-muted-foreground" :stroke-width="1.75" />
      </div>
      <div class="min-w-0 flex-1">
        <Popover v-if="info && source.status === 'ready'">
          <PopoverTrigger as-child>
            <button
              type="button"
              class="line-clamp-2 max-w-full text-left text-[14px] leading-tight font-semibold hover:underline"
              :title="source.displayName"
            >
              {{ hasName ? info.name : shortFilename }}
            </button>
          </PopoverTrigger>
          <PopoverContent align="start" class="w-[300px] p-3 text-[12px]">
            <div class="mb-2 text-[13px] font-semibold">{{ source.displayName }}</div>
            <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
              <dt class="text-muted-foreground">File</dt>
              <dd class="selectable font-mono text-[11.5px] break-all">{{ info.path }}</dd>
              <dt class="text-muted-foreground">Size</dt>
              <dd>{{ formatSize(info.size) }}</dd>
              <template v-if="bootModes">
                <dt class="text-muted-foreground">Boots with</dt>
                <dd>{{ bootModes }}</dd>
              </template>
              <template v-if="info.label">
                <dt class="text-muted-foreground">Volume</dt>
                <dd class="selectable">{{ info.label }}</dd>
              </template>
              <template v-if="info.editions?.length">
                <dt class="text-muted-foreground">Editions</dt>
                <dd>
                  <ul class="max-h-[84px] overflow-y-auto">
                    <li v-for="e in info.editions" :key="e">{{ e }}</li>
                  </ul>
                </dd>
              </template>
            </dl>
          </PopoverContent>
        </Popover>
        <div v-else class="truncate text-[14px] font-semibold">
          {{ source.status === 'probing' ? 'Reading image…' : shortFilename }}
        </div>
        <div
          v-if="source.status === 'ready' ? hasName : source.status === 'unusable' && info"
          class="truncate text-[12px] text-muted-foreground"
          :title="source.filename ?? ''"
        >
          {{ source.status === 'ready' ? shortFilename : `ISO image · ${formatSize(info!.size)}` }}
        </div>
        <div v-else-if="source.status === 'probing'" class="truncate text-[12px] text-muted-foreground">
          {{ shortFilename }}
        </div>
      </div>
    </div>

    <div v-if="source.status === 'ready'" class="mt-auto flex flex-wrap gap-1.5">
      <span v-for="c in chips" :key="c" class="chip">{{ c }}</span>
    </div>
    <div
      v-else-if="source.status === 'unusable'"
      class="mt-auto flex items-start gap-2 rounded-md bg-danger-bg px-2.5 py-2 text-[12px] leading-snug text-danger"
    >
      <TriangleAlert class="mt-px size-4 shrink-0" :stroke-width="1.75" />
      <span class="selectable line-clamp-3">Can't use this image: {{ source.reason }}</span>
    </div>
  </div>
</template>
