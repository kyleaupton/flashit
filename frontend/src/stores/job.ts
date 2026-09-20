import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import { StartJob, CancelJob } from '@flashit/service/jobsservice'
import { Status } from '@/types'
import type { Job, JobEvent, StartJobRequest, StepState } from '@/types'

export const useJobStore = defineStore('job', () => {
  const currentJobId = ref<string | null>(null)
  const jobs = ref<Map<string, Job>>(new Map())
  const error = ref<string | null>(null)
  const isStarting = ref(false)
  const isCancelling = ref(false)
  const steps = ref<StepState[]>([])

  let eventUnsubscribe: (() => void) | null = null

  // Buffer for events that arrive before job ID is set
  let pendingEvents: JobEvent[] = []

  const currentJob = computed(() =>
    currentJobId.value ? jobs.value.get(currentJobId.value) ?? null : null
  )

  const status = computed((): Status | null => currentJob.value?.Status ?? null)

  const isRunning = computed(() => status.value === Status.StatusRunning)
  const isComplete = computed(() => status.value === Status.StatusSucceeded)
  const isFailed = computed(() => status.value === Status.StatusFailed)
  const isPending = computed(() => status.value === Status.StatusPending)
  const isCancelled = computed(() => status.value === Status.StatusCancelled)

  function handleJobEvent(event: JobEvent): void {
    // If we're starting a job but don't have the ID yet, buffer the event
    if (isStarting.value && !currentJobId.value) {
      pendingEvents.push(event)
      return
    }

    if (event.jobId !== currentJobId.value) {
      return
    }

    processJobEvent(event)
  }

  function processJobEvent(event: JobEvent): void {
    switch (event.type) {
      case 'state': {
        const job = jobs.value.get(event.jobId)
        if (job) {
          const stateMap: Record<string, Status> = {
            pending: Status.StatusPending,
            running: Status.StatusRunning,
            succeeded: Status.StatusSucceeded,
            failed: Status.StatusFailed,
            cancelled: Status.StatusCancelled,
          }
          job.Status = stateMap[event.message] ?? job.Status

          if (event.error) {
            error.value = event.error
            const runningStep = steps.value.find((s) => s.status === 'running')
            if (runningStep) {
              runningStep.status = 'failed'
            }
          }

          if (event.message === 'cancelled' || event.message === 'succeeded' || event.message === 'failed') {
            isCancelling.value = false
          }

          if (event.message === 'succeeded') {
            toast.success('Flash complete!', {
              description: 'Your bootable drive is ready to use.',
            })
          } else if (event.message === 'failed') {
            toast.error('Flash failed', {
              description: event.error || 'Check the error details for more information.',
            })
          } else if (event.message === 'cancelled') {
            toast.info('Flash cancelled', {
              description: 'The operation was cancelled.',
            })
          }
        }
        break
      }

      case 'step-start': {
        const step = steps.value.find((s) => s.key === event.step)
        if (step) {
          step.status = 'running'
          step.message = null
          step.progress = 0
        }
        break
      }

      case 'step-end': {
        const step = steps.value.find((s) => s.key === event.step)
        if (step) {
          step.status = 'completed'
          step.progress = 100
        }
        break
      }

      case 'progress': {
        const runningStep = steps.value.find((s) => s.status === 'running')
        if (runningStep) {
          runningStep.progress = event.percent
          if (event.message) {
            runningStep.message = event.message
          }
        }
        break
      }

      case 'error': {
        error.value = event.error || event.message
        const runningStep = steps.value.find((s) => s.status === 'running')
        if (runningStep) {
          runningStep.status = 'failed'
        }
        break
      }
    }
  }

  function subscribeToEvents(): void {
    if (eventUnsubscribe) return

    eventUnsubscribe = Events.On('job:event', (ev: Events.WailsEvent) => {
      handleJobEvent(ev.data as JobEvent)
    })
  }

  function unsubscribeFromEvents(): void {
    eventUnsubscribe?.()
    eventUnsubscribe = null
  }

  async function startJob(request: StartJobRequest): Promise<string> {
    isStarting.value = true
    error.value = null
    steps.value = []
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

      jobs.value.set(response.jobId, {
        ID: response.jobId,
        Status: Status.StatusPending,
      })

      // Replay any events that arrived before we had the job ID
      const eventsToReplay = pendingEvents.filter((e) => e.jobId === response.jobId)
      pendingEvents = []
      for (const event of eventsToReplay) {
        processJobEvent(event)
      }

      return response.jobId
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to start job'
      throw e
    } finally {
      isStarting.value = false
    }
  }

  async function cancelJob(): Promise<boolean> {
    if (!currentJobId.value) {
      return false
    }

    isCancelling.value = true
    try {
      const cancelled = await CancelJob(currentJobId.value)
      if (!cancelled) {
        isCancelling.value = false
      }
      return cancelled
    } catch (e) {
      console.error('Failed to cancel job:', e)
      isCancelling.value = false
      return false
    }
  }

  function clearCurrentJob(): void {
    currentJobId.value = null
    error.value = null
    steps.value = []
  }

  function $reset(): void {
    unsubscribeFromEvents()
    currentJobId.value = null
    jobs.value.clear()
    error.value = null
    isStarting.value = false
    isCancelling.value = false
    steps.value = []
    pendingEvents = []
  }

  return {
    currentJobId,
    jobs,
    error,
    isStarting,
    isCancelling,
    steps,
    currentJob,
    status,
    isRunning,
    isComplete,
    isFailed,
    isPending,
    isCancelled,
    subscribeToEvents,
    unsubscribeFromEvents,
    startJob,
    cancelJob,
    clearCurrentJob,
    $reset,
  }
})
