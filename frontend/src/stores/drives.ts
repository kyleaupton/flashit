import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { ListDrives } from '@flashit/service/drivesservice'
import type { Drive } from '@/types'

export const useDrivesStore = defineStore('drives', () => {
  const drives = ref<Drive[]>([])
  const selectedDriveId = ref<string | null>(null)
  const isLoading = ref(false)
  const error = ref<string | null>(null)
  let refreshIntervalId: ReturnType<typeof setInterval> | null = null

  const selectedDrive = computed(() =>
    drives.value.find((d) => d.Device === selectedDriveId.value) ?? null
  )

  const removableDrives = computed(() =>
    drives.value.filter((d) => d.IsRemovable || d.IsEjectable)
  )

  const hasDrives = computed(() => removableDrives.value.length > 0)

  async function fetchDrives(): Promise<void> {
    isLoading.value = true
    error.value = null

    try {
      const result = await ListDrives()
      drives.value = result

      // Clear selection if selected drive was unplugged
      if (selectedDriveId.value && !result.find((d) => d.Device === selectedDriveId.value)) {
        selectedDriveId.value = null
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to fetch drives'
      console.error('Failed to fetch drives:', e)
    } finally {
      isLoading.value = false
    }
  }

  function selectDrive(deviceId: string | null): void {
    selectedDriveId.value = deviceId
  }

  function startAutoRefresh(intervalMs: number = 3000): void {
    stopAutoRefresh()
    fetchDrives()
    refreshIntervalId = setInterval(fetchDrives, intervalMs)
  }

  function stopAutoRefresh(): void {
    if (refreshIntervalId) {
      clearInterval(refreshIntervalId)
      refreshIntervalId = null
    }
  }

  function $reset(): void {
    stopAutoRefresh()
    drives.value = []
    selectedDriveId.value = null
    isLoading.value = false
    error.value = null
  }

  return {
    drives,
    selectedDriveId,
    isLoading,
    error,
    selectedDrive,
    removableDrives,
    hasDrives,
    fetchDrives,
    selectDrive,
    startAutoRefresh,
    stopAutoRefresh,
    $reset,
  }
})
