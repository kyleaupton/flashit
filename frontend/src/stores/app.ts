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
    if (jobStore.isStarting || jobStore.isPending || jobStore.isRunning) return 'running'
    if (sourceStore.isUsable && drivesStore.selectedDrive) return 'target-selected'
    if (sourceStore.hasSource) return 'source-probed'
    return 'idle'
  })

  const isSelecting = computed(() =>
    ['idle', 'source-probed', 'target-selected'].includes(state.value)
  )
  const isFinished = computed(() =>
    ['done', 'failed', 'cancelled'].includes(state.value)
  )
  const canFlash = computed(() => state.value === 'target-selected')

  return { state, isSelecting, isFinished, canFlash }
})
