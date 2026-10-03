<script setup lang="ts">
const props = defineProps<{ command: string }>()
const copied = ref(false)

async function copy() {
  await navigator.clipboard.writeText(props.command)
  copied.value = true
  setTimeout(() => (copied.value = false), 1500)
}
</script>

<template>
  <button type="button" class="mx-auto inline-flex items-start gap-4 whitespace-pre text-left leading-6 rounded-lg border border-default bg-elevated/40 px-5 py-3 font-mono text-sm text-toned transition hover:text-highlighted" :aria-label="`Copy ${command}`" @click="copy">
    {{ command }}
    <UIcon :name="copied ? 'i-lucide-check' : 'i-lucide-copy'" class="mt-1 size-4 shrink-0" />
  </button>
</template>
