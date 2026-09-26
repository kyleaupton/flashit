import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import { Check, Info, Install, Restart } from '@flashit/service/updaterservice'
import type { Release } from '@/types'

export type UpdatePhase =
  | 'idle'
  | 'checking'
  | 'available'
  | 'downloading'
  | 'verifying'
  | 'installing'
  | 'ready'
  | 'error'

interface WailsRelease {
  version: string
  name?: string
  notes?: string
  publishedAt?: string
}

function fromWails(r: WailsRelease | null | undefined): Release | null {
  if (!r?.version) return null
  return { version: r.version, name: r.name ?? '', notes: r.notes ?? '', publishedAt: r.publishedAt ?? '' }
}

export const useUpdateStore = defineStore('update', () => {
  const enabled = ref(false)
  const version = ref('')
  const phase = ref<UpdatePhase>('idle')
  const release = ref<Release | null>(null)
  const written = ref(0)
  const total = ref(0)
  const error = ref<string | null>(null)
  const sheetOpen = ref(false)
  // A check the user asked for: it opens the sheet or says "latest".
  let manual = false
  let subscribed = false

  const available = computed(() => release.value !== null && phase.value !== 'checking')
  const percent = computed(() => (total.value > 0 ? (written.value / total.value) * 100 : 0))

  function subscribe(): void {
    if (subscribed) return
    subscribed = true
    Events.On('wails:updater:check-started', () => {
      phase.value = 'checking'
      error.value = null
    })
    Events.On('wails:updater:update-available', (ev: Events.WailsEvent) => {
      release.value = fromWails(ev.data as WailsRelease)
      phase.value = 'available'
      if (manual) sheetOpen.value = true
      manual = false
    })
    Events.On('wails:updater:no-update', () => {
      release.value = null
      phase.value = 'idle'
      if (manual) toast(`FlashIt ${version.value} is the latest version`)
      manual = false
    })
    Events.On('wails:updater:download-started', () => {
      phase.value = 'downloading'
      written.value = 0
      total.value = 0
    })
    Events.On('wails:updater:download-progress', (ev: Events.WailsEvent) => {
      const p = ev.data as { written: number; total: number }
      written.value = p.written
      total.value = p.total
    })
    Events.On('wails:updater:verifying', () => (phase.value = 'verifying'))
    Events.On('wails:updater:installing', () => (phase.value = 'installing'))
    Events.On('wails:updater:update-ready', () => (phase.value = 'ready'))
    Events.On('wails:updater:error', (ev: Events.WailsEvent) => {
      const info = ev.data as { stage: string; message: string }
      error.value = info.message
      if (info.stage === 'check' && !manual && phase.value !== 'available') {
        // A silent launch check that failed is not worth interrupting for.
        phase.value = release.value ? 'available' : 'idle'
        return
      }
      phase.value = 'error'
      if (manual) sheetOpen.value = true
      manual = false
    })
    Events.On('updater:check-requested', () => {
      checkNow()
    })
  }

  async function load(): Promise<void> {
    subscribe()
    try {
      const info = await Info()
      enabled.value = info.enabled
      version.value = info.version
      if (!info.enabled) return
      release.value = info.pending ?? null
      switch (info.state) {
        case 'available':
        case 'downloading':
        case 'verifying':
        case 'installing':
        case 'ready':
        case 'checking':
          phase.value = info.state
      }
    } catch (e) {
      console.error('updater info failed', e)
    }
  }

  async function checkNow(): Promise<void> {
    if (!enabled.value || phase.value === 'checking') return
    if (release.value && phase.value !== 'error') {
      sheetOpen.value = true
      return
    }
    manual = true
    try {
      await Check()
    } catch (e) {
      // The error event already moved the sheet to its error state.
      console.error('update check failed', e)
    }
  }

  async function install(): Promise<void> {
    error.value = null
    phase.value = 'downloading'
    written.value = 0
    total.value = 0
    try {
      await Install()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      phase.value = 'error'
    }
  }

  async function retry(): Promise<void> {
    if (release.value) return install()
    manual = true
    try {
      await Check()
    } catch {
      /* reported through the error event */
    }
  }

  async function restart(): Promise<void> {
    try {
      await Restart()
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      toast.error('Could not restart', { description: error.value })
    }
  }

  return {
    enabled,
    version,
    phase,
    release,
    written,
    total,
    percent,
    error,
    sheetOpen,
    available,
    load,
    checkNow,
    install,
    retry,
    restart,
  }
})
