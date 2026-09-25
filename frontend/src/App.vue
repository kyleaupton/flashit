<script setup lang="ts">
import { onMounted } from 'vue'
import { useColorMode } from '@vueuse/core'
import { useDrivesStore, useSourceStore, useJobStore, useAppStore, useUpdateStore } from '@/stores'
import { Button } from '@/components/ui/button'
import SourceDropzone from '@/components/SourceDropzone.vue'
import DriveSelector from '@/components/DriveSelector.vue'
import ProgressPanel from '@/components/ProgressPanel.vue'
import StatusAlert from '@/components/StatusAlert.vue'
import FlashButton from '@/components/FlashButton.vue'
import UpdateBanner from '@/components/UpdateBanner.vue'
import { Toaster } from '@/components/ui/sonner'
import { OpenPrivacySettings } from '@flashit/service/privservice'
import { toast } from 'vue-sonner'
import 'vue-sonner/style.css'

const drivesStore = useDrivesStore()
const sourceStore = useSourceStore()
const jobStore = useJobStore()
const appStore = useAppStore()
const updateStore = useUpdateStore()

async function handleStartJob() {
  if (!sourceStore.source || !drivesStore.selectedDrive) return

  try {
    await jobStore.startJob({
      SourcePath: sourceStore.source.path,
      DriveID: drivesStore.selectedDrive.Device,
    })
  } catch (e) {
    toast.error('Could not start', {
      description: e instanceof Error ? e.message : String(e),
    })
  }
}

function handleReset() {
  jobStore.clearCurrentJob()
  sourceStore.clearSource()
  drivesStore.selectDrive(null)
}

onMounted(() => {
  const mode = useColorMode()
  mode.value = 'dark'

  drivesStore.startAutoRefresh()
  sourceStore.subscribeToDrops()
  jobStore.subscribeToEvents()
  void updateStore.init()
})
</script>

<template>
  <div class="flex flex-col h-screen bg-background text-foreground">
    <header class="app-header p-4 text-center">
      <h1 class="text-xl font-semibold m-0">FlashIt</h1>
    </header>

    <div class="px-4 pb-3 empty:hidden">
      <UpdateBanner />
    </div>

    <Toaster position="bottom-center" />

    <!-- Selection View: Source + Drive panels -->
    <main v-if="appStore.isSelecting" class="flex-1 flex flex-col justify-between px-4 pb-4 min-h-0">
      <div
        class="flex gap-4 w-full mx-auto transition-all duration-400 ease-out min-h-0"
        :class="sourceStore.hasSource ? 'max-w-[800px]' : 'max-w-[500px]'"
      >
        <div class="flex-1 min-w-0 min-h-0 transition-all duration-400 ease-out">
          <SourceDropzone />
        </div>
        <Transition name="slide-in">
          <div v-if="sourceStore.isUsable" class="flex-1 min-w-0 min-h-0 flex flex-col">
            <DriveSelector />
          </div>
        </Transition>
      </div>

      <div
        v-if="sourceStore.hasSource"
        class="w-full max-w-[800px] mx-auto mt-4"
      >
        <FlashButton
          :loading="jobStore.isStarting"
          :disabled="!appStore.canFlash"
          @click="handleStartJob"
        />
      </div>
    </main>

    <!-- Progress/Status View: Centered -->
    <main v-else class="flex-1 flex flex-col items-center justify-center px-4 pb-4">
      <div class="w-full max-w-[400px] flex flex-col gap-6">
        <ProgressPanel v-if="appStore.state === 'running'" />

        <template v-if="appStore.isFinished">
          <StatusAlert
            :status="appStore.state"
            :error="jobStore.error"
            :error-code="jobStore.errorCode"
            :warnings="jobStore.warnings"
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
