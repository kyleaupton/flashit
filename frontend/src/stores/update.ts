import { ref, computed } from 'vue'
import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { toast } from 'vue-sonner'
import { CheckNow, GetState, OpenReleasePage, Restart } from '@flashit/service/updateservice'
import { Mode, State, Status } from '@flashit/update/models'
import { useJobStore } from './job'

const dismissedKey = 'flashit.update.dismissed'

function readDismissed(): string | null {
  try {
    return localStorage.getItem(dismissedKey)
  } catch {
    return null
  }
}

function writeDismissed(version: string | null): void {
  try {
    if (version) localStorage.setItem(dismissedKey, version)
    else localStorage.removeItem(dismissedKey)
  } catch {
    // Dismissal then lasts for this session only.
  }
}

export const useUpdateStore = defineStore('update', () => {
  const state = ref<State>(new State({ mode: Mode.Off, status: Status.StatusIdle }))
  const dismissed = ref<string | null>(readDismissed())
  const isRestarting = ref(false)
  const jobStore = useJobStore()

  const found = computed(() =>
    (state.value.status === Status.StatusAvailable || state.value.status === Status.StatusReady ||
      state.value.status === Status.StatusRestarting) ? state.value.version ?? null : null
  )
  const visible = computed(() => !!found.value && found.value !== dismissed.value)
  const isReady = computed(() =>
    state.value.status === Status.StatusReady || state.value.status === Status.StatusRestarting
  )
  // The Go side refuses too; this only keeps the button honest.
  const flashInProgress = computed(
    () => jobStore.isStarting || jobStore.isPending || jobStore.isRunning
  )

  let unsubscribe: (() => void)[] = []
  let sawEvent = false
  // The loading toast of a check asked for from the menu, while it runs.
  let manualToast: string | number | null = null

  async function init(): Promise<void> {
    if (unsubscribe.length) return
    unsubscribe = [
      Events.On('update:state', (ev: Events.WailsEvent) => {
        sawEvent = true
        state.value = State.createFrom(ev.data)
        if (manualToast !== null && state.value.status === Status.StatusDownloading) {
          toast.loading(`Downloading FlashIt ${state.value.version}…`, { id: manualToast })
        }
      }),
      Events.On('update:check-requested', () => {
        void checkNow()
      }),
    ]
    try {
      const s = await GetState()
      // An event that arrived meanwhile is newer than this snapshot.
      if (!sawEvent) state.value = s
    } catch (e) {
      console.error('update state:', e)
    }
  }

  async function checkNow(): Promise<void> {
    if (manualToast !== null) return
    const id = toast.loading('Checking for updates…')
    manualToast = id
    try {
      const s = await CheckNow()
      state.value = s
      switch (s.status) {
        case Status.StatusUpToDate:
          toast.success(`FlashIt ${s.current} is up to date`, { id })
          break
        case Status.StatusError:
          toast.error('Could not check for updates', { id, description: s.error })
          break
        case Status.StatusAvailable:
        case Status.StatusReady:
        case Status.StatusRestarting:
          // Asked for by hand, so show it even if this version was dismissed.
          dismissed.value = null
          writeDismissed(null)
          toast.dismiss(id)
          break
        case Status.StatusChecking:
        case Status.StatusDownloading:
          // A background check was already under way; its result arrives as
          // a state event.
          toast.info('Already checking for updates', { id })
          break
        default:
          toast.dismiss(id)
      }
    } catch (e) {
      toast.error('Could not check for updates', {
        id,
        description: e instanceof Error ? e.message : String(e),
      })
    } finally {
      manualToast = null
    }
  }

  async function restart(): Promise<void> {
    isRestarting.value = true
    try {
      await Restart()
    } catch (e) {
      isRestarting.value = false
      toast.error('Could not restart', {
        description: e instanceof Error ? e.message : String(e),
      })
    }
  }

  async function openRelease(): Promise<void> {
    try {
      await OpenReleasePage()
    } catch (e) {
      toast.error('Could not open the release page', {
        description: e instanceof Error ? e.message : String(e),
      })
    }
  }

  function dismiss(): void {
    if (!found.value) return
    dismissed.value = found.value
    writeDismissed(found.value)
  }

  return {
    state,
    found,
    visible,
    isReady,
    isRestarting,
    flashInProgress,
    init,
    checkNow,
    restart,
    openRelease,
    dismiss,
  }
})
