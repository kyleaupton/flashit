import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { ListDrives } from '@flashit/service/drivesservice'
import type { Drive } from '@/types'

const justConnectedMs = 10_000

/** Size, model and serial: what tells a stick apart from the one that had its device path before. */
export function sameDrive(a: Drive, b: Drive): boolean {
  return a.Device === b.Device && a.SizeBytes === b.SizeBytes && a.Model === b.Model && a.Serial === b.Serial
}

function driveKey(d: Drive): string {
  return [d.Device, d.SizeBytes, d.Model, d.Serial].join('|')
}

export const useDrivesStore = defineStore('drives', () => {
  const drives = ref<Drive[]>([])
  // A list so several drives can be flashed at once later; capped at one today.
  const selectedDriveIds = ref<string[]>([])
  // The drives as the user picked them. A selected drive that leaves the
  // list, or comes back as something else, stays here as disconnected until
  // it is picked again.
  const picked = ref<Record<string, Drive>>({})
  const disconnected = ref<Record<string, boolean>>({})
  // When each drive first appeared after the initial listing.
  const appearedAt = ref<Record<string, number>>({})
  const loaded = ref(false)
  const error = ref<string | null>(null)
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null

  const removableDrives = computed(() => drives.value.filter((d) => d.IsRemovable || d.IsEjectable))

  const selectedPick = computed(() => {
    const id = selectedDriveIds.value[0]
    return id ? picked.value[id] ?? null : null
  })
  const isDisconnected = computed(() => {
    const id = selectedDriveIds.value[0]
    return !!id && !!disconnected.value[id]
  })
  /** The selected drive while it is still connected and unchanged. */
  const selectedDrive = computed(() => (isDisconnected.value ? null : selectedPick.value))

  function isJustConnected(d: Drive): boolean {
    const t = appearedAt.value[driveKey(d)]
    return t !== undefined && now.value - t < justConnectedMs
  }

  async function fetchDrives(): Promise<void> {
    try {
      const result = await ListDrives()
      const before = new Set(drives.value.map(driveKey))
      const t = Date.now()
      now.value = t
      if (loaded.value) {
        for (const d of result) {
          const k = driveKey(d)
          if (!before.has(k)) appearedAt.value[k] = t
        }
      }
      drives.value = result
      loaded.value = true
      error.value = null

      for (const id of selectedDriveIds.value) {
        const was = picked.value[id]
        const listed = result.find((d) => d.Device === id)
        if (!listed || !sameDrive(listed, was)) disconnected.value[id] = true
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not list drives'
    }
  }

  function selectDrive(drive: Drive | null): void {
    picked.value = {}
    disconnected.value = {}
    selectedDriveIds.value = drive ? [drive.Device] : []
    if (drive) picked.value[drive.Device] = { ...drive }
  }

  function startAutoRefresh(intervalMs = 3000): void {
    if (timer) return
    fetchDrives()
    timer = setInterval(fetchDrives, intervalMs)
  }

  return {
    drives,
    removableDrives,
    selectedDriveIds,
    selectedPick,
    selectedDrive,
    isDisconnected,
    loaded,
    error,
    isJustConnected,
    fetchDrives,
    selectDrive,
    startAutoRefresh,
  }
})
