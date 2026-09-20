import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { ListInstallers } from '@flashit/service/jobsservice'
import { formatSize } from '@/lib/utils'
import type { InstallerMeta, SourceInfo } from '@/types'

export const useSourceStore = defineStore('source', () => {
  const source = ref<SourceInfo | null>(null)
  const installers = ref<InstallerMeta[]>([])
  const isAnalyzing = ref(false)
  const error = ref<string | null>(null)

  const hasSource = computed(() => source.value !== null)

  const filename = computed(() => source.value?.filename ?? null)

  const detectedInstaller = computed(() => {
    if (!source.value?.installerID) return null
    return installers.value.find((i) => i.ID === source.value!.installerID) ?? null
  })

  const fileSizeFormatted = computed(() =>
    source.value?.sizeBytes ? formatSize(source.value.sizeBytes) : null
  )

  async function loadInstallers(): Promise<void> {
    try {
      installers.value = await ListInstallers()
    } catch (e) {
      console.error('Failed to load installers:', e)
    }
  }

  function detectInstallerFromFilename(filename: string): InstallerMeta | null {
    const lower = filename.toLowerCase()

    // Pattern matching - can be extended as more installers are added
    if (lower.includes('ubuntu') || lower.includes('linux')) {
      return installers.value.find((i) => i.ID === 'linux') ?? null
    }
    if (lower.includes('windows') || lower.includes('win10') || lower.includes('win11')) {
      return installers.value.find((i) => i.ID === 'windows') ?? null
    }

    return null
  }

  async function setSource(path: string): Promise<void> {
    isAnalyzing.value = true
    error.value = null

    try {
      // Ensure installers are loaded
      if (installers.value.length === 0) {
        await loadInstallers()
      }

      const filename = path.split('/').pop() ?? path
      const detected = detectInstallerFromFilename(filename)

      source.value = {
        path,
        filename,
        sizeBytes: 0, // Could be populated by backend in future
        installerID: detected?.ID ?? null,
        installerName: detected?.Name ?? null,
        detectedTargets: detected?.Targets ?? [],
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to analyze source'
      source.value = null
    } finally {
      isAnalyzing.value = false
    }
  }

  function clearSource(): void {
    source.value = null
    error.value = null
  }

  function $reset(): void {
    source.value = null
    isAnalyzing.value = false
    error.value = null
  }

  return {
    source,
    installers,
    isAnalyzing,
    error,
    hasSource,
    filename,
    detectedInstaller,
    fileSizeFormatted,
    loadInstallers,
    setSource,
    clearSource,
    $reset,
  }
})
