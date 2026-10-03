<script setup lang="ts">
import { joinURL } from 'ufo'

const props = defineProps<{ src: string, alt: string }>()
const src = computed(() => joinURL(useRuntimeConfig().app.baseURL, props.src))
</script>

<template>
  <!-- The frame is the GIF's Catppuccin Mocha background, so frame and recording read as one window. -->
  <div class="mx-auto w-full max-w-3xl overflow-hidden rounded-xl bg-[#1e1e2e] shadow-2xl shadow-black/60 ring-1 ring-white/10">
    <div class="flex items-center gap-1.5 px-4 py-3">
      <span class="size-2.5 rounded-full bg-white/15" />
      <span class="size-2.5 rounded-full bg-white/15" />
      <span class="size-2.5 rounded-full bg-white/15" />
    </div>
    <img :src="src" :alt="alt" class="block w-full">
  </div>
</template>
