import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { Probe } from '@flashit/service/sourcesservice'
import { formatSize } from '@/lib/utils'
import { SourceKind } from '@/types'
import type { SourceInfo } from '@/types'

export const useSourceStore = defineStore('source', () => {
  const source = ref<SourceInfo | null>(null)
  const isAnalyzing = ref(false)
  // Why the last pick or drop was not taken; shown in the dropzone.
  const error = ref<string | null>(null)

  let dropUnsubscribe: (() => void) | null = null
  // Two quick drops probe concurrently; only the latest result may land.
  let probeToken = 0

  const hasSource = computed(() => source.value !== null)
  const filename = computed(() => source.value?.path.split('/').pop() ?? null)
  const isUsable = computed(
    () => source.value !== null && source.value.kind !== SourceKind.Unknown
  )
  const kindLabel = computed(() => {
    switch (source.value?.kind) {
      case SourceKind.LinuxISO:
        return 'Linux'
      case SourceKind.WindowsISO:
        return 'Windows'
      default:
        return null
    }
  })
  const fileSizeFormatted = computed(() =>
    source.value?.size ? formatSize(source.value.size) : null
  )

  async function setSource(path: string): Promise<void> {
    const token = ++probeToken
    isAnalyzing.value = true
    error.value = null

    try {
      const info = await Probe(path)
      if (token !== probeToken) return
      source.value = info
    } catch (e) {
      if (token !== probeToken) return
      error.value = e instanceof Error ? e.message : 'Failed to read the image'
      source.value = null
    } finally {
      if (token === probeToken) isAnalyzing.value = false
    }
  }

  function clearSource(): void {
    source.value = null
    error.value = null
  }

  // The backend relays Wails' WindowFilesDropped as files:dropped; the
  // runtime only reports drops on data-file-drop-target elements.
  function subscribeToDrops(): void {
    if (dropUnsubscribe) return
    dropUnsubscribe = Events.On('files:dropped', (ev: Events.WailsEvent) => {
      const files = (ev.data as { files?: string[] })?.files ?? []
      if (files.length !== 1) {
        error.value = 'Drop one image file'
        return
      }
      setSource(files[0])
    })
  }

  function unsubscribeFromDrops(): void {
    dropUnsubscribe?.()
    dropUnsubscribe = null
  }

  function $reset(): void {
    unsubscribeFromDrops()
    source.value = null
    isAnalyzing.value = false
    error.value = null
  }

  return {
    source,
    isAnalyzing,
    error,
    hasSource,
    filename,
    isUsable,
    kindLabel,
    fileSizeFormatted,
    setSource,
    clearSource,
    subscribeToDrops,
    unsubscribeFromDrops,
    $reset,
  }
})
