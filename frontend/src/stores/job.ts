import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { StartJob, CancelJob } from '@flashit/service/jobsservice'
import { Status } from '@/types'
import type { DriveProgress, JobEvent, StartJobRequest, StepState } from '@/types'

// Plain words for the step keys the installers use.
const phases: Record<string, string> = {
  preparing: 'Preparing',
  unmounting: 'Unmounting the drive',
  'writing-iso': 'Writing image',
  ejecting: 'Ejecting the drive',
  'opening-iso': 'Opening the image',
  formatting: 'Formatting the drive',
  'analyzing-wim': 'Checking install.wim',
  'copying-files': 'Copying Windows files',
  'splitting-wim': 'Splitting install.wim',
  finalizing: 'Finishing up',
}

export function phaseName(step: StepState | null | undefined): string {
  if (!step) return 'Starting'
  return phases[step.key] ?? step.name
}

// Helper refusals that come before anything is written to the drive.
const untouchedCodes = new Set(['cancelled', 'not_removable', 'system_disk', 'insufficient_capacity', 'tcc_denied'])

const maxLogLines = 500
// Weight of the newest sample in the smoothed speed.
const speedSmoothing = 0.2

export interface LogLine {
  at: number
  text: string
  level: 'info' | 'warning' | 'error'
}

export const useJobStore = defineStore('job', () => {
  const currentJobId = ref<string | null>(null)
  const driveId = ref<string | null>(null)
  const status = ref<Status | null>(null)
  const error = ref<string | null>(null)
  // The helper's error code behind a failure, when it came from it.
  const errorCode = ref<string | null>(null)
  const isStarting = ref(false)
  const isCancelling = ref(false)
  // Set while the OS asks the user to approve the privileged step; the drive
  // is untouched until the first progress or step-end arrives.
  const isAuthorizing = ref(false)
  // Things the user must still act on after a job that succeeded, such as a
  // drive that could not be ejected because something had it open.
  const warnings = ref<string[]>([])
  const steps = ref<StepState[]>([])
  const logs = ref<LogLine[]>([])
  // Keyed by drive ID; one entry until several drives can be flashed at once.
  const progress = ref<Record<string, DriveProgress>>({})
  const startedAt = ref<number | null>(null)
  const finishedAt = ref<number | null>(null)
  // Percent of the step that was running when the job failed.
  const failedAt = ref<{ step: StepState; percent: number } | null>(null)

  let eventUnsubscribe: (() => void) | null = null
  let pendingEvents: JobEvent[] = []
  let lastSample: { at: number; bytes: number; percent: number } | null = null

  const isRunning = computed(() => status.value === Status.StatusRunning)
  const isComplete = computed(() => status.value === Status.StatusSucceeded)
  const isFailed = computed(() => status.value === Status.StatusFailed)
  const isPending = computed(() => status.value === Status.StatusPending)
  const isCancelled = computed(() => status.value === Status.StatusCancelled)
  const isActive = computed(() => isStarting.value || isPending.value || isRunning.value)
  // The helper refused before touching the drive because macOS denied
  // FlashIt access to removable volumes; the user can grant it and retry.
  const tccDenied = computed(() => errorCode.value === 'tcc_denied')
  const driveTouched = computed(() => !(errorCode.value && untouchedCodes.has(errorCode.value)))
  const runningStep = computed(() => steps.value.find((s) => s.status === 'running') ?? null)
  const current = computed(() => (driveId.value ? progress.value[driveId.value] ?? null : null))

  function log(text: string, level: LogLine['level'] = 'info'): void {
    logs.value.push({ at: Date.now(), text, level })
    if (logs.value.length > maxLogLines) logs.value.splice(0, logs.value.length - maxLogLines)
  }

  function resetProgress(step: string | null): void {
    if (!driveId.value) return
    progress.value[driveId.value] = {
      step,
      percent: 0,
      bytes: null,
      total: null,
      speed: null,
      eta: null,
      startedAt: Date.now(),
    }
    lastSample = null
  }

  function updateProgress(event: JobEvent): void {
    const p = current.value
    if (!p) return
    const at = Date.now()
    p.percent = event.percent ?? 0
    if (event.total) {
      p.bytes = event.bytes ?? 0
      p.total = event.total
    }
    if (lastSample && at - lastSample.at >= 250) {
      const dt = (at - lastSample.at) / 1000
      if (p.bytes !== null && p.total) {
        const rate = (p.bytes - lastSample.bytes) / dt
        if (rate >= 0) p.speed = p.speed === null ? rate : p.speed + speedSmoothing * (rate - p.speed)
        if (p.speed) p.eta = (p.total - p.bytes) / p.speed
      } else {
        // No byte counts: time left from how fast the percentage moves.
        const elapsed = (at - p.startedAt) / 1000
        if (p.percent > 1) p.eta = (elapsed * (100 - p.percent)) / p.percent
      }
      lastSample = { at, bytes: p.bytes ?? 0, percent: p.percent }
    } else if (!lastSample) {
      lastSample = { at, bytes: p.bytes ?? 0, percent: p.percent }
    }
  }

  function handleJobEvent(event: JobEvent): void {
    if (isStarting.value && !currentJobId.value) {
      pendingEvents.push(event)
      return
    }
    if (event.jobId !== currentJobId.value) return
    processJobEvent(event)
  }

  function processJobEvent(event: JobEvent): void {
    // The prompt is over once the step produces anything else.
    if (event.type !== 'authorizing') isAuthorizing.value = false

    switch (event.type) {
      case 'state': {
        const stateMap: Record<string, Status> = {
          pending: Status.StatusPending,
          running: Status.StatusRunning,
          succeeded: Status.StatusSucceeded,
          failed: Status.StatusFailed,
          cancelled: Status.StatusCancelled,
        }
        status.value = stateMap[event.message] ?? status.value
        if (event.error) {
          error.value = event.error
          errorCode.value = event.code ?? null
          const step = runningStep.value
          if (step) {
            failedAt.value = { step: { ...step }, percent: current.value?.percent ?? 0 }
            step.status = 'failed'
          }
          log(event.error, 'error')
        }
        if (['cancelled', 'succeeded', 'failed'].includes(event.message)) {
          isCancelling.value = false
          finishedAt.value = Date.now()
          log(`Job ${event.message}`)
        }
        break
      }
      case 'step-start': {
        const step = steps.value.find((s) => s.key === event.step)
        if (step) {
          step.status = 'running'
          step.message = null
          step.progress = 0
          log(step.name)
        }
        resetProgress(event.step)
        break
      }
      case 'step-end': {
        const step = steps.value.find((s) => s.key === event.step)
        if (step) {
          step.status = event.error ? 'failed' : 'completed'
          step.progress = event.error ? step.progress : 100
        }
        break
      }
      case 'authorizing':
        isAuthorizing.value = true
        log('Waiting for approval')
        break
      case 'warning':
        if (event.message && !warnings.value.includes(event.message)) {
          warnings.value.push(event.message)
          log(event.message, 'warning')
        }
        break
      case 'log':
        if (event.message) log(event.message)
        break
      case 'progress': {
        const step = runningStep.value
        if (step) {
          step.progress = event.percent
          if (event.message) step.message = event.message
        }
        updateProgress(event)
        break
      }
      case 'error':
        error.value = event.error || event.message
        errorCode.value = event.code ?? null
        if (runningStep.value) runningStep.value.status = 'failed'
        log(error.value, 'error')
        break
    }
  }

  function subscribeToEvents(): void {
    if (eventUnsubscribe) return
    eventUnsubscribe = Events.On('job:event', (ev: Events.WailsEvent) => {
      handleJobEvent(ev.data as JobEvent)
    })
  }

  async function startJob(request: StartJobRequest): Promise<string> {
    clearCurrentJob()
    isStarting.value = true
    driveId.value = request.DriveID
    startedAt.value = Date.now()
    resetProgress(null)
    pendingEvents = []

    try {
      subscribeToEvents()
      const response = await StartJob(request)
      currentJobId.value = response.jobId
      steps.value = (response.stepInfos || []).map((info) => ({
        key: info.key,
        name: info.name,
        hasProgress: info.hasProgress,
        status: 'pending' as const,
        progress: 0,
        message: null,
      }))
      status.value = Status.StatusPending

      const replay = pendingEvents.filter((e) => e.jobId === response.jobId)
      pendingEvents = []
      for (const event of replay) processJobEvent(event)
      return response.jobId
    } catch (e) {
      // A refused start is the caller's to show, not a failed job.
      driveId.value = null
      progress.value = {}
      throw e
    } finally {
      isStarting.value = false
    }
  }

  async function cancelJob(): Promise<boolean> {
    if (!currentJobId.value) return false
    isCancelling.value = true
    try {
      const cancelled = await CancelJob(currentJobId.value)
      if (!cancelled) isCancelling.value = false
      return cancelled
    } catch (e) {
      console.error('Failed to cancel job:', e)
      isCancelling.value = false
      return false
    }
  }

  function clearCurrentJob(): void {
    currentJobId.value = null
    driveId.value = null
    status.value = null
    error.value = null
    errorCode.value = null
    isAuthorizing.value = false
    isCancelling.value = false
    warnings.value = []
    steps.value = []
    logs.value = []
    progress.value = {}
    startedAt.value = null
    finishedAt.value = null
    failedAt.value = null
    lastSample = null
  }

  return {
    currentJobId,
    driveId,
    status,
    error,
    errorCode,
    isStarting,
    isCancelling,
    isAuthorizing,
    warnings,
    steps,
    logs,
    progress,
    current,
    runningStep,
    startedAt,
    finishedAt,
    failedAt,
    tccDenied,
    driveTouched,
    isRunning,
    isComplete,
    isFailed,
    isPending,
    isCancelled,
    isActive,
    subscribeToEvents,
    startJob,
    cancelJob,
    clearCurrentJob,
  }
})
