<script setup lang="ts">
import { computed, ref } from 'vue'
import { toast } from 'vue-sonner'
import { CircleCheck, CircleX, KeyRound, Loader2, TriangleAlert } from 'lucide-vue-next'
import { OpenPrivacySettings } from '@flashit/service/privservice'
import ProgressBar from '@/components/ProgressBar.vue'
import { phaseName } from '@/stores/job'
import { useAppStore, useDrivesStore, useJobStore, useSourceStore } from '@/stores'
import { driveName as nameOf, formatDuration, formatSize } from '@/lib/utils'
import { fitEnd, fontOf, textWidth } from '@/composables/fit'

const emit = defineEmits<{ details: [] }>()

const app = useAppStore()
const drives = useDrivesStore()
const source = useSourceStore()
const job = useJobStore()

const driveName = computed(() => (drives.selectedPick ? nameOf(drives.selectedPick) : 'the drive'))

// Cut in script, not CSS: a truncated flex item keeps the space before its
// ellipsis, which read as "USB … and flash".
const ERASE_MAX = 280
const eraseBtn = ref<HTMLElement | null>(null)
const eraseLabel = computed(() => {
  const name = driveName.value
  const el = eraseBtn.value
  if (!el) return `Erase ${name} and flash`
  const font = fontOf(el)
  const s = getComputedStyle(el)
  const room = ERASE_MAX - parseFloat(s.paddingLeft) - parseFloat(s.paddingRight) - 2 - textWidth('Erase  and flash', font)
  return `Erase ${fitEnd(name, room, font)} and flash`
})

// Copy for the helper's closed error codes; anything else shows the raw
// message, with the full text in Details.
const codeCopy: Record<string, { title: string; text: string }> = {
  cancelled: {
    title: 'Not approved',
    text: 'The system prompt was dismissed, so nothing was written.',
  },
  not_removable: {
    title: 'Drive refused',
    text: 'The system does not report this drive as removable.',
  },
  system_disk: {
    title: 'Drive refused',
    text: 'This drive holds the running system.',
  },
  insufficient_capacity: {
    title: 'Drive too small',
    text: 'The image is larger than the drive.',
  },
  device_busy: {
    title: 'Drive in use',
    text: 'Close anything using the drive, or unplug and reinsert it, then try again.',
  },
}

async function start() {
  const d = drives.selectedDrive
  const s = source.source
  if (!d || !s || !app.canFlash) return
  try {
    await job.startJob({
      SourcePath: s.path,
      DriveID: d.Device,
      SizeBytes: d.SizeBytes,
      Model: d.Model,
      Serial: d.Serial,
    })
  } catch (e) {
    toast.error('Could not start', { description: e instanceof Error ? e.message : String(e) })
  }
}

async function retry() {
  job.clearCurrentJob()
  await start()
}

function flashAnother() {
  job.clearCurrentJob()
  drives.selectDrive(null)
}

function startOver() {
  job.clearCurrentJob()
  drives.selectDrive(null)
  source.clearSource()
}

defineExpose({ start })

const p = computed(() => job.current)
const phase = computed(() => phaseName(job.runningStep))
const showsPercent = computed(() => !!job.runningStep?.hasProgress)

const transferLine = computed(() => {
  const cur = p.value
  if (!cur || !showsPercent.value) return 'Working…'
  const parts: string[] = []
  if (cur.total && cur.bytes !== null) {
    const unit = cur.total >= 1e9 ? 1e9 : 1e6
    const fmt = (n: number) => (unit === 1e9 ? (n / unit).toFixed(1) : Math.round(n / unit).toString())
    parts.push(`${fmt(cur.bytes)} of ${fmt(cur.total)} ${unit === 1e9 ? 'GB' : 'MB'}`)
  }
  if (cur.speed) parts.push(`${formatSize(cur.speed)}/s`)
  // The estimate covers this step only; the Windows copy is followed by a
  // split of about the same length, so it would promise too early an end.
  const lastLongStep = !(job.runningStep?.key === 'copying-files' && source.needsSplit)
  if (cur.eta !== null && cur.percent < 100 && lastLongStep) parts.push(`about ${formatDuration(cur.eta)} left`)
  return parts.join(' · ') || 'Starting…'
})

const failure = computed(() => {
  const copy = (job.errorCode && codeCopy[job.errorCode]) || null
  const f = job.failedAt
  let title = copy?.title ?? 'Flash failed'
  if (!copy && f && f.step.hasProgress && f.percent > 0) {
    title = `${phaseName(f.step)} stopped at ${Math.floor(f.percent)}%`
  }
  const tail = job.driveTouched ? ` ${driveName.value} is not bootable now.` : ' Nothing was written.'
  const text = copy?.text ?? job.error ?? 'Something went wrong.'
  return { title, text: (/[.!?]$/.test(text) ? text : `${text}.`) + tail }
})

const doneLine = computed(() => {
  if (job.warnings.length) return job.warnings[0]
  const took = job.startedAt && job.finishedAt ? ` in ${formatDuration((job.finishedAt - job.startedAt) / 1000)}` : ''
  const size = source.source?.size ? `Wrote ${formatSize(source.source.size)}${took}. ` : ''
  return `${size}You can remove it now.`
})

const tccText = 'Allow FlashIt under Privacy & Security › Files and Folders › Removable Volumes, then try again.'

const cancelledText = computed(() =>
  job.driveTouched ? `${driveName.value} may not be bootable.` : 'Nothing was written to the drive.',
)

const canRetry = computed(() => !!drives.selectedDrive && source.isUsable)

const hint = computed(() => {
  if (source.status === 'unusable') return 'Choose a different image to continue.'
  if (source.status === 'probing') return 'Reading the image…'
  if (drives.isDisconnected) return 'The drive was disconnected. Choose a drive again.'
  if (source.isUsable && drives.selectedDrive && !app.fits) return 'Choose a larger drive.'
  return 'Choose an image and a drive, in either order.'
})
</script>

<template>
  <div class="flex h-[108px] shrink-0 flex-col justify-center gap-2.5">
    <!-- Waiting on the OS prompt -->
    <template v-if="app.state === 'running' && job.isAuthorizing">
      <div class="flex min-w-0 items-center justify-between gap-3">
        <div class="flex min-w-0 items-center gap-2">
          <KeyRound class="size-4 shrink-0" :stroke-width="1.75" />
          <span class="truncate text-[14px] font-semibold">Waiting for approval</span>
        </div>
        <span class="truncate text-[12px] text-muted-foreground">Approve in the system dialog</span>
      </div>
      <ProgressBar indeterminate />
      <div class="flex min-w-0 items-center justify-between gap-3">
        <span class="min-w-0 truncate text-[12px] text-muted-foreground">The drive has not been touched yet.</span>
        <button type="button" class="btn btn-default" :disabled="job.isCancelling" @click="job.cancelJob()">
          Cancel
        </button>
      </div>
    </template>

    <template v-else-if="app.state === 'running'">
      <div class="flex min-w-0 items-baseline justify-between gap-3">
        <span class="min-w-0 truncate text-[14px] font-semibold">{{ job.isCancelling ? 'Stopping' : phase }}</span>
        <span v-if="showsPercent" class="shrink-0 text-[22px] leading-none font-semibold tabular-nums">
          {{ Math.floor(p?.percent ?? 0) }}%
        </span>
      </div>
      <ProgressBar :value="p?.percent ?? 0" :indeterminate="!showsPercent || job.isCancelling" />
      <div class="flex min-w-0 items-center justify-between gap-3">
        <span class="min-w-0 truncate text-[12px] text-muted-foreground tabular-nums" :title="transferLine">{{ transferLine }}</span>
        <div class="flex shrink-0 gap-2">
          <button type="button" class="btn btn-default" @click="emit('details')">Details</button>
          <button type="button" class="btn btn-default" :disabled="job.isCancelling" @click="job.cancelJob()">
            <Loader2 v-if="job.isCancelling" class="size-3.5 animate-spin" />
            Cancel
          </button>
        </div>
      </div>
    </template>

    <div v-else-if="app.state === 'done'" class="flex min-w-0 items-center justify-between gap-3">
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <TriangleAlert v-if="job.warnings.length" class="size-5 shrink-0 text-warn" :stroke-width="1.75" />
        <CircleCheck v-else class="size-5 shrink-0 text-success" :stroke-width="1.75" />
        <div class="min-w-0">
          <div class="truncate text-[14px] font-semibold">Done. The drive is bootable.</div>
          <div class="line-clamp-2 text-[12px] leading-snug text-muted-foreground" :title="doneLine">{{ doneLine }}</div>
        </div>
      </div>
      <div class="flex shrink-0 gap-2">
        <button type="button" class="btn btn-default" @click="startOver">Start over</button>
        <button type="button" class="btn btn-primary" @click="flashAnother">Flash another</button>
      </div>
    </div>

    <div v-else-if="app.state === 'failed' && job.tccDenied" class="flex min-w-0 items-center justify-between gap-3">
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <KeyRound class="size-5 shrink-0 text-primary" :stroke-width="1.75" />
        <div class="min-w-0">
          <div class="truncate text-[14px] font-semibold">Permission needed</div>
          <div class="line-clamp-2 text-[12px] leading-snug text-muted-foreground" :title="tccText">{{ tccText }}</div>
        </div>
      </div>
      <div class="flex shrink-0 flex-col gap-1.5">
        <button type="button" class="btn btn-primary" @click="OpenPrivacySettings()">Open Privacy Settings</button>
        <button type="button" class="btn btn-default" :disabled="!canRetry" @click="retry">Try again</button>
      </div>
    </div>

    <div v-else-if="app.state === 'failed'" class="flex min-w-0 items-center justify-between gap-3">
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <CircleX class="size-5 shrink-0 text-danger" :stroke-width="1.75" />
        <div class="min-w-0">
          <div class="truncate text-[14px] font-semibold" :title="failure.title">{{ failure.title }}</div>
          <div class="selectable line-clamp-2 text-[12px] leading-snug break-words text-muted-foreground" :title="failure.text">
            {{ failure.text }}
          </div>
        </div>
      </div>
      <div class="flex shrink-0 gap-2">
        <button type="button" class="btn btn-default" @click="emit('details')">Show log</button>
        <button type="button" class="btn btn-primary" :disabled="!canRetry" @click="retry">Try again</button>
      </div>
    </div>

    <div v-else-if="app.state === 'cancelled'" class="flex min-w-0 items-center justify-between gap-3">
      <div class="flex min-w-0 flex-1 items-center gap-2.5">
        <CircleX class="size-5 shrink-0 text-muted-foreground" :stroke-width="1.75" />
        <div class="min-w-0">
          <div class="truncate text-[14px] font-semibold">Stopped</div>
          <div class="line-clamp-2 text-[12px] leading-snug text-muted-foreground" :title="cancelledText">
            {{ cancelledText }}
          </div>
        </div>
      </div>
      <div class="flex shrink-0 gap-2">
        <button type="button" class="btn btn-default" @click="startOver">Start over</button>
        <button type="button" class="btn btn-primary" :disabled="!canRetry" @click="retry">Try again</button>
      </div>
    </div>

    <div v-else-if="app.state === 'target-selected' && app.canFlash" class="flex min-w-0 items-center justify-between gap-3">
      <span class="min-w-0 truncate text-[12px] text-muted-foreground">Nothing is erased until you approve.</span>
      <button
        type="button"
        ref="eraseBtn"
        class="btn btn-primary max-w-[280px] min-w-0"
        :disabled="job.isStarting"
        :title="`Erase ${driveName} and flash`"
        @click="start"
        @keydown.enter.prevent
      >
        <Loader2 v-if="job.isStarting" class="size-3.5 shrink-0 animate-spin" />
        <span class="min-w-0 truncate">{{ eraseLabel }}</span>
      </button>
    </div>

    <div v-else class="flex min-w-0 items-center justify-between gap-3">
      <span class="line-clamp-2 min-w-0 text-[13px] leading-snug text-muted-foreground">{{ hint }}</span>
      <button type="button" class="btn btn-primary min-w-24" disabled>Flash</button>
    </div>
  </div>
</template>
