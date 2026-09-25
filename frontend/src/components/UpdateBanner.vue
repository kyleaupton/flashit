<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { useUpdateStore } from '@/stores'

const update = useUpdateStore()
</script>

<template>
  <div
    v-if="update.visible"
    class="mx-auto flex w-fit max-w-[calc(100%-2rem)] items-center gap-3 rounded-full border bg-card py-1 pl-4 pr-1 text-sm text-card-foreground shadow-xs"
    role="status"
  >
    <template v-if="update.isReady">
      <span class="truncate">FlashIt {{ update.found }} is ready</span>
      <span v-if="update.flashInProgress" class="text-xs text-muted-foreground whitespace-nowrap">
        Restart after the flash finishes
      </span>
      <Button
        size="sm"
        class="h-7 rounded-full"
        :disabled="update.flashInProgress || update.isRestarting"
        @click="update.restart()"
      >
        {{ update.isRestarting ? 'Restarting…' : 'Restart to update' }}
      </Button>
    </template>
    <template v-else>
      <span class="truncate">FlashIt {{ update.found }} is available</span>
      <Button size="sm" variant="secondary" class="h-7 rounded-full" @click="update.openRelease()">
        View release
      </Button>
    </template>
    <Button
      size="icon-sm"
      variant="ghost"
      class="size-7 rounded-full text-muted-foreground"
      aria-label="Dismiss"
      :disabled="update.isRestarting"
      @click="update.dismiss()"
    >
      <X />
    </Button>
  </div>
</template>
