import { computed } from 'vue'
import { defineStore } from 'pinia'
import { useDrivesStore } from './drives'
import { useSourceStore } from './source'
import { useJobStore } from './job'
import type { AppState } from '@/types'

/** The one place the app's state machine is derived from the other stores. */
export const useAppStore = defineStore('app', () => {
  const drivesStore = useDrivesStore()
  const sourceStore = useSourceStore()
  const jobStore = useJobStore()

  const state = computed((): AppState => {
    if (jobStore.isFailed) return 'failed'
    if (jobStore.isCancelled) return 'cancelled'
    if (jobStore.isComplete) return 'done'
    if (jobStore.isActive) return 'running'
    if (sourceStore.isUsable && drivesStore.selectedDrive) return 'target-selected'
    if (sourceStore.status !== 'empty') return 'source-probed'
    return 'idle'
  })

  const isRunning = computed(() => state.value === 'running')
  const isFinished = computed(() => ['done', 'failed', 'cancelled'].includes(state.value))
  const canFlash = computed(
    () => state.value === 'target-selected' && fits.value
  )
  // The picker disables a drive too small for the image, but a probe can
  // land after the pick.
  const fits = computed(() => {
    const d = drivesStore.selectedDrive
    const s = sourceStore.source
    return !d || !s || d.SizeBytes >= s.size
  })

  // Changing the image or drive after a job ends starts a new round.
  function leaveFinished(): void {
    if (isFinished.value) jobStore.clearCurrentJob()
  }

  return { state, isRunning, isFinished, canFlash, fits, leaveFinished }
})
