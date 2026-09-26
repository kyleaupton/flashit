import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Dialogs } from '@wailsio/runtime'
import { Probe } from '@flashit/service/sourcesservice'
import { basename } from '@/lib/utils'
import { SourceKind } from '@/types'
import type { SourceInfo, SourceStatus } from '@/types'

// Microsoft's install.wim over FAT32's 4 GiB file limit is split into .swm parts.
const fat32MaxFile = 4 * 1024 * 1024 * 1024 - 1

export const useSourceStore = defineStore('source', () => {
  const status = ref<SourceStatus>('empty')
  const source = ref<SourceInfo | null>(null)
  // The path being probed or last picked, so the slot can name the file
  // even when the probe could not read it.
  const path = ref<string | null>(null)
  // Why the image cannot be used: the probe's reason, or why it failed.
  const reason = ref<string | null>(null)

  // Two quick drops probe concurrently; only the latest result may land.
  let probeToken = 0

  const filename = computed(() => (path.value ? basename(path.value) : null))
  const isUsable = computed(() => status.value === 'ready')
  const isWindows = computed(() => source.value?.kind === SourceKind.WindowsISO)
  const needsSplit = computed(() => isWindows.value && (source.value?.wimSize ?? 0) > fat32MaxFile)
  const displayName = computed(() => source.value?.name || filename.value || '')

  async function setSource(p: string): Promise<void> {
    const token = ++probeToken
    path.value = p
    source.value = null
    reason.value = null
    status.value = 'probing'

    try {
      const info = await Probe(p)
      if (token !== probeToken) return
      source.value = info
      if (info.kind === SourceKind.Unknown) {
        reason.value = info.reason ?? 'This is not an image FlashIt can write.'
        status.value = 'unusable'
      } else {
        status.value = 'ready'
      }
    } catch (e) {
      if (token !== probeToken) return
      reason.value = e instanceof Error ? e.message : String(e)
      status.value = 'unusable'
    }
  }

  async function choose(): Promise<void> {
    const picked = await Dialogs.OpenFile({
      Title: 'Choose an image',
      CanChooseFiles: true,
      CanChooseDirectories: false,
      AllowsOtherFiletypes: true,
      Filters: [
        { DisplayName: 'Disk images', Pattern: '*.iso;*.img' },
        { DisplayName: 'All files', Pattern: '*.*' },
      ],
    })
    if (picked) await setSource(picked)
  }

  function clearSource(): void {
    probeToken++
    status.value = 'empty'
    source.value = null
    path.value = null
    reason.value = null
  }

  return {
    status,
    source,
    path,
    reason,
    filename,
    displayName,
    isUsable,
    isWindows,
    needsSplit,
    setSource,
    choose,
    clearSource,
  }
})
