<script setup lang="ts">
import { computed } from 'vue'
import { HardDrive, Loader2, TriangleAlert } from 'lucide-vue-next'
import { useDrivesStore } from '@/stores'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Badge } from '@/components/ui/badge'
import { formatSize } from '@/lib/utils'

const drivesStore = useDrivesStore()

const drives = computed(() => drivesStore.removableDrives)
const selectedDriveId = computed({
  get: () => drivesStore.selectedDriveId ?? '',
  set: (value) => drivesStore.selectDrive(value || null)
})
const hasDrives = computed(() => drivesStore.hasDrives)
const isLoading = computed(() => drivesStore.isLoading)
</script>

<template>
  <Card class="flex flex-col min-h-0 h-full">
    <CardHeader class="pb-2 shrink-0">
      <div class="flex items-center justify-between">
        <CardTitle class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Target Drive
        </CardTitle>
        <Loader2 v-if="isLoading" class="h-3 w-3 animate-spin text-muted-foreground" />
      </div>
      <p v-if="hasDrives" class="flex items-center gap-1.5 text-xs text-destructive">
        <TriangleAlert class="h-3.5 w-3.5" />
        All data will be erased
      </p>
    </CardHeader>
    <CardContent class="flex-1 min-h-0 overflow-y-auto">
      <!-- Drive List -->
      <RadioGroup v-if="hasDrives" v-model="selectedDriveId" class="flex flex-col gap-2">
        <label
          v-for="drive in drives"
          :key="drive.Device"
          class="flex cursor-pointer items-start gap-3 rounded-md border p-3 transition-colors hover:border-primary"
          :class="selectedDriveId === drive.Device ? 'border-primary bg-accent' : 'border-border'"
        >
          <RadioGroupItem :value="drive.Device" class="mt-0.5 shrink-0" />
          <HardDrive
            class="mt-0.5 h-4 w-4 shrink-0"
            :class="selectedDriveId === drive.Device ? 'text-primary' : 'text-muted-foreground'"
          />
          <div class="min-w-0 flex-1">
            <div class="mb-1 flex items-center justify-between gap-2">
              <span class="truncate font-medium">{{ drive.Model || 'Unknown Drive' }}</span>
              <Badge variant="outline" class="shrink-0">
                {{ formatSize(drive.SizeBytes) }}
              </Badge>
            </div>
            <span class="font-mono text-xs text-muted-foreground">{{ drive.Device }}</span>
          </div>
        </label>
      </RadioGroup>

      <!-- Empty State -->
      <div v-if="!hasDrives" class="flex flex-col items-center py-8 text-center">
        <HardDrive class="mb-4 h-8 w-8 text-muted-foreground opacity-50" />
        <p class="font-medium text-muted-foreground">No removable drives detected</p>
        <p class="text-sm text-muted-foreground/70">Insert a USB drive to continue</p>
      </div>
    </CardContent>
  </Card>
</template>
