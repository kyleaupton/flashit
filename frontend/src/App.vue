<script setup lang="ts">
import { onMounted, computed } from 'vue'
import { useColorMode } from '@vueuse/core'
import { useDrivesStore, useSourceStore, useJobStore } from '@/stores'
import type { AppState } from '@/types'
import { Button } from '@/components/ui/button'
import SourceDropzone from '@/components/SourceDropzone.vue'
import DriveSelector from '@/components/DriveSelector.vue'
import ProgressPanel from '@/components/ProgressPanel.vue'
import StatusAlert from '@/components/StatusAlert.vue'
import FlashButton from '@/components/FlashButton.vue'
import { Toaster } from '@/components/ui/sonner'
import { OpenPrivacySettings } from '@flashit/service/privservice'
import { toast } from 'vue-sonner'
import 'vue-sonner/style.css'


// Initialize stores
const drivesStore = useDrivesStore()
const sourceStore = useSourceStore()
const jobStore = useJobStore()

// Combine store states into overall app state
const appState = computed((): AppState => {
  if (jobStore.isFailed || jobStore.error) return 'error'
  if (jobStore.isRunning || jobStore.isPending) return 'in-progress'
  if (jobStore.isCancelled) return 'cancelled'
  if (jobStore.isComplete) return 'complete'
  if (sourceStore.hasSource && drivesStore.selectedDrive) return 'ready'
  if (sourceStore.hasSource) return 'source-only'
  return 'empty'
})

// Show the selection panels (source + drive) vs centered progress/status view
const showSelectionView = computed(() => {
  return !['in-progress', 'complete', 'cancelled', 'error'].includes(appState.value)
})

// Start job handler
async function handleStartJob() {
  if (!sourceStore.source || !drivesStore.selectedDrive || !sourceStore.detectedInstaller) {
    toast.warning('Please select a source and drive')
    return
  }

  try {
    await jobStore.startJob({
      InstallerID: sourceStore.detectedInstaller.ID,
      SourceLocal: sourceStore.source.path,
      DriveID: drivesStore.selectedDrive.Device,
    })
  } catch (e) {
    toast.error('Failed to start job', {
      description: e instanceof Error ? e.message : String(e),
    })
  }
}

// Reset to start over
function handleReset() {
  jobStore.clearCurrentJob()
  sourceStore.clearSource()
  drivesStore.selectDrive(null)
}

onMounted(() => {
  const mode = useColorMode()
  mode.value = 'dark';

  drivesStore.startAutoRefresh()
  sourceStore.loadInstallers()
  jobStore.subscribeToEvents()
})
</script>

<template>
  <div class="flex flex-col h-screen bg-background text-foreground">
    <header class="app-header p-4 text-center">
      <h1 class="text-xl font-semibold m-0">FlashIt</h1>
    </header>

    <Toaster position="bottom-center" />

    <!-- Selection View: Source + Drive panels -->
    <main v-if="showSelectionView" class="flex-1 flex flex-col justify-between px-4 pb-4 min-h-0">
      <div
        class="flex gap-4 w-full mx-auto transition-all duration-400 ease-out min-h-0"
        :class="sourceStore.hasSource ? 'max-w-[800px]' : 'max-w-[500px]'"
      >
        <div class="flex-1 min-w-0 min-h-0 transition-all duration-400 ease-out">
          <SourceDropzone />
        </div>
        <Transition name="slide-in">
          <div v-if="sourceStore.hasSource" class="flex-1 min-w-0 min-h-0 flex flex-col">
            <DriveSelector />
          </div>
        </Transition>
      </div>

      <!-- Flash Button (shown when source selected, disabled until drive selected) -->
      <div
        v-if="sourceStore.hasSource"
        class="w-full max-w-[800px] mx-auto mt-4"
      >
        <FlashButton
          :loading="jobStore.isStarting"
          :disabled="appState !== 'ready'"
          @click="handleStartJob"
        />
      </div>
    </main>

    <!-- Progress/Status View: Centered -->
    <main v-else class="flex-1 flex flex-col items-center justify-center px-4 pb-4">
      <div class="w-full max-w-[400px] flex flex-col gap-6">
        <ProgressPanel v-if="appState === 'in-progress'" />

        <template v-if="appState === 'complete' || appState === 'cancelled' || appState === 'error'">
          <StatusAlert
            :status="appState"
            :error="jobStore.error"
            :tcc-denied="jobStore.tccDenied"
            @open-settings="OpenPrivacySettings()"
            @retry="handleStartJob"
          />

          <Button
            size="lg"
            variant="secondary"
            class="w-full h-12 text-base"
            @click="handleReset"
          >
            Start Over
          </Button>
        </template>
      </div>
    </main>
  </div>
</template>

<style scoped>
/* Wails window drag region */
.app-header {
  --wails-draggable: drag;
}

/* Slide-in transition for drive panel */
.slide-in-enter-active {
  transition: all 0.4s ease;
  transition-delay: 0.1s;
}

.slide-in-leave-active {
  transition: all 0.3s ease;
}

.slide-in-enter-from,
.slide-in-leave-to {
  opacity: 0;
  transform: translateX(30px);
}
</style>
