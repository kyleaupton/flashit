<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Browser } from '@wailsio/runtime'
import { ArrowUpCircle, Ellipsis, Loader2 } from 'lucide-vue-next'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { OpenLogFolder, Quit, ShowAbout } from '@flashit/service/appservice'
import { useJobStore, useUpdateStore } from '@/stores'
import { isMac } from '@/lib/utils'

const releasesURL = 'https://github.com/kyleaupton/flashit/releases'
const issueURL = 'https://github.com/kyleaupton/flashit/issues/new'

const update = useUpdateStore()
const job = useJobStore()

const showPill = computed(() => update.available && !job.isActive)
const menuOpen = ref(false)
// The check item keeps the menu open to show its spinner; the result is a
// sheet or a toast, so the menu closes when the check ends.
watch(
  () => update.phase,
  (now, before) => {
    if (before === 'checking' && now !== 'checking') menuOpen.value = false
  },
)
const versionLabel = computed(() => (update.version ? `FlashIt ${update.version}` : 'FlashIt'))
</script>

<template>
  <header
    class="titlebar relative flex h-[38px] shrink-0 items-center gap-2 border-b bg-titlebar pr-2.5"
    :class="isMac ? 'pl-[78px]' : 'pl-3'"
  >
    <span
      v-if="isMac"
      class="pointer-events-none absolute inset-x-0 text-center text-[13px] font-semibold text-muted-foreground"
    >
      FlashIt
    </span>
    <div class="ml-auto flex min-w-0 items-center gap-1.5">
      <button
        v-if="showPill"
        type="button"
        class="no-drag flex h-6 max-w-[180px] min-w-0 items-center gap-1 rounded-full bg-primary px-2.5 text-[12px] font-medium text-primary-foreground hover:brightness-110"
        :title="`Update to ${update.release?.version}`"
        @click="update.checkNow()"
      >
        <ArrowUpCircle class="size-3.5 shrink-0" />
        <span class="truncate">Update to {{ update.release?.version }}</span>
      </button>
      <DropdownMenu v-model:open="menuOpen">
        <DropdownMenuTrigger as-child>
          <button
            type="button"
            aria-label="More"
            class="no-drag relative flex h-6 w-7 items-center justify-center rounded-md hover:bg-foreground/10 data-[state=open]:bg-foreground/10"
          >
            <Ellipsis class="size-[18px]" />
            <span
              v-if="update.available"
              class="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-primary"
            />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="w-[220px] text-[13px]">
          <DropdownMenuLabel class="truncate font-normal text-[12px] text-muted-foreground" :title="versionLabel">
            {{ versionLabel }}
          </DropdownMenuLabel>
          <DropdownMenuItem v-if="update.enabled" @select.prevent="update.checkNow()">
            <span class="min-w-0 truncate">{{ update.available ? `Update to ${update.release?.version}…` : 'Check for updates…' }}</span>
            <Loader2 v-if="update.phase === 'checking'" class="ml-auto animate-spin" />
          </DropdownMenuItem>
          <DropdownMenuItem v-else @select="Browser.OpenURL(releasesURL)">Releases page</DropdownMenuItem>
          <DropdownMenuItem @select="OpenLogFolder()">Open log folder</DropdownMenuItem>
          <DropdownMenuItem @select="Browser.OpenURL(issueURL)">Report a problem…</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem @select="ShowAbout()">About FlashIt</DropdownMenuItem>
          <DropdownMenuItem @select="Quit()">
            Quit
            <DropdownMenuShortcut>{{ isMac ? '⌘Q' : 'Ctrl+Q' }}</DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  </header>
</template>

<style scoped>
.titlebar {
  --wails-draggable: drag;
}
.no-drag {
  --wails-draggable: no-drag;
}
</style>
