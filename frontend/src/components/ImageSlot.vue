<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { FileIcon, Loader2, TriangleAlert } from 'lucide-vue-next'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useAppStore, useSourceStore } from '@/stores'
import { fitMiddle, fontOf, useWidth } from '@/composables/fit'
import { formatSize, languageName } from '@/lib/utils'
import { SourceKind } from '@/types'

const source = useSourceStore()
const app = useAppStore()

const info = computed(() => source.source)
const locked = computed(() => app.isRunning)
const popoverOpen = ref(false)

function choose() {
  app.leaveFinished()
  source.choose()
}

const bootModes = computed(() => {
  const i = info.value
  if (!i) return null
  if (i.bios && i.uefi) return 'UEFI + BIOS'
  if (i.uefi) return 'UEFI'
  if (i.bios) return 'BIOS'
  return null
})

// In priority order; the kind only when there is no name to say it.
const chips = computed(() => {
  const i = info.value
  if (!i) return []
  if (i.kind === SourceKind.LinuxISO) {
    return [i.name ? null : 'Linux', i.arch, bootModes.value].filter(Boolean) as string[]
  }
  const editions = i.editions?.length ?? 0
  return [
    i.name ? null : 'Windows',
    i.arch,
    editions > 1 ? `${editions} editions` : null,
    i.language || null,
  ].filter(Boolean) as string[]
})

const chipRow = ref<HTMLElement | null>(null)
const chipRowWidth = useWidth(chipRow)
// Every chip plus a "+N" template, rendered out of sight, so the fit is
// measured from the real .chip styles.
const chipMeasure = ref<HTMLElement | null>(null)
// Bumped once the measuring row has rendered the current chips.
const measured = ref(0)
watch(chips, () => measured.value++, { flush: 'post' })
const visibleChips = computed(() => {
  void measured.value
  const all = chips.value
  const width = chipRowWidth.value
  const measure = chipMeasure.value
  if (!chipRow.value || !measure || !width) return all.length
  const widths = Array.from(measure.children, (c) => (c as HTMLElement).offsetWidth)
  const moreWidth = widths.pop() ?? 0
  const gap = parseFloat(getComputedStyle(chipRow.value).columnGap) || 0
  let used = 0
  for (let n = 0; n < all.length; n++) {
    used += (n ? gap : 0) + widths[n]
    const more = n < all.length - 1 ? gap + moreWidth : 0
    if (used + more > width) return n
  }
  return all.length
})
const hiddenChips = computed(() => chips.value.length - visibleChips.value)

const nameLine = ref<HTMLElement | null>(null)
const nameLineWidth = useWidth(nameLine)
const sizeSuffix = computed(() => (info.value?.size ? ` · ${formatSize(info.value.size)}` : ''))
// Filename cut in the middle so its version and extension stay visible,
// followed by the size.
const fileLine = computed(() => {
  if (!info.value?.name && source.status !== 'probing') return formatSize(info.value?.size ?? 0)
  const f = source.filename ?? ''
  if (!nameLine.value) return f + sizeSuffix.value
  return fitMiddle(f, sizeSuffix.value, nameLineWidth.value, fontOf(nameLine.value)) + sizeSuffix.value
})

const titleEl = ref<HTMLElement | null>(null)
const titleWidth = useWidth(titleEl)
// Without a name the filename is the title: cut in the middle to about two
// lines, so its version and extension survive, and not repeated below.
const title = computed(() => {
  if (source.status === 'probing') return 'Reading image…'
  if (info.value?.name) return info.value.name
  const f = source.filename ?? ''
  if (!titleEl.value) return f
  return fitMiddle(f, '', titleWidth.value * 1.8, fontOf(titleEl.value))
})
</script>

<template>
  <div v-if="source.status === 'empty'" class="slot-empty">
    <FileIcon class="size-7 text-muted-foreground" :stroke-width="1.75" />
    <button type="button" class="btn btn-default" :disabled="locked" @click="choose">Choose image…</button>
    <span class="text-[12px] text-muted-foreground">or drop an .iso or .img here</span>
  </div>

  <div v-else class="slot gap-2.5" :class="source.status === 'unusable' && 'border-[1.5px] border-danger'">
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
        <Popover v-model:open="popoverOpen">
          <PopoverTrigger as-child :disabled="source.status !== 'ready'">
            <button
              ref="titleEl"
              type="button"
              class="line-clamp-2 w-full max-w-full text-left text-[14px] leading-tight font-semibold break-words enabled:hover:underline"
              :disabled="source.status !== 'ready'"
              :title="info?.name || source.filename || ''"
            >
              {{ title }}
            </button>
          </PopoverTrigger>
          <PopoverContent v-if="info" align="start" :collision-padding="8" class="max-h-(--reka-popover-content-available-height) w-[300px] overflow-y-auto p-3 text-[12px]">
            <div class="mb-2 line-clamp-2 text-[13px] font-semibold break-words">{{ source.displayName }}</div>
            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
              <dt class="text-muted-foreground">File</dt>
              <dd class="selectable max-h-[48px] overflow-y-auto font-mono text-[11.5px] break-all">{{ info.path }}</dd>
              <dt class="text-muted-foreground">Size</dt>
              <dd>{{ formatSize(info.size) }}</dd>
              <template v-if="info.arch">
                <dt class="text-muted-foreground">Architecture</dt>
                <dd>{{ info.arch }}</dd>
              </template>
              <template v-if="bootModes">
                <dt class="text-muted-foreground">Boots with</dt>
                <dd>{{ bootModes }}</dd>
              </template>
              <template v-if="info.language">
                <dt class="text-muted-foreground">Language</dt>
                <dd>{{ languageName(info.language) }}</dd>
              </template>
              <template v-if="info.label">
                <dt class="text-muted-foreground">Volume</dt>
                <dd class="selectable truncate" :title="info.label">{{ info.label }}</dd>
              </template>
              <template v-if="info.editions?.length">
                <dt class="text-muted-foreground">Editions</dt>
                <dd>
                  <ul class="max-h-[84px] overflow-y-auto">
                    <li v-for="(e, i) in info.editions" :key="i" class="truncate" :title="e">{{ e }}</li>
                  </ul>
                </dd>
              </template>
            </dl>
            <p v-if="source.needsSplit" class="mt-2 border-t pt-2 text-muted-foreground">
              install.wim is over 4 GB, so it will be split into parts that fit FAT32.
            </p>
          </PopoverContent>
        </Popover>
        <div
          ref="nameLine"
          class="overflow-hidden text-[12px] whitespace-nowrap text-muted-foreground"
          :title="`${source.filename ?? ''}${sizeSuffix}`"
        >
          {{ source.status === 'unusable' && !info ? '' : fileLine }}
        </div>
      </div>
    </div>

    <div v-if="source.status === 'ready'" aria-hidden="true" class="pointer-events-none invisible absolute top-0 left-0">
      <div ref="chipMeasure" class="flex gap-1.5">
        <span v-for="c in chips" :key="c" class="chip shrink-0">{{ c }}</span>
        <span class="chip shrink-0">+{{ chips.length }}</span>
      </div>
    </div>
    <div v-if="source.status === 'ready'" ref="chipRow" class="mt-auto flex min-w-0 flex-nowrap gap-1.5 overflow-hidden">
      <span v-for="c in chips.slice(0, visibleChips)" :key="c" class="chip shrink-0">{{ c }}</span>
      <button
        v-if="hiddenChips > 0"
        type="button"
        class="chip shrink-0 hover:bg-accent"
        :title="chips.slice(visibleChips).join(', ')"
        @click="popoverOpen = true"
      >
        +{{ hiddenChips }}
      </button>
    </div>
    <div
      v-else-if="source.status === 'unusable'"
      class="mt-auto flex min-w-0 items-start gap-2 rounded-md bg-danger-bg px-2.5 py-2 text-[12px] leading-snug text-danger"
      :title="`Can't use this image: ${source.reason}`"
    >
      <TriangleAlert class="mt-px size-4 shrink-0" :stroke-width="1.75" />
      <span class="selectable line-clamp-3 min-w-0 break-words">Can't use this image: {{ source.reason }}</span>
    </div>
  </div>
</template>
