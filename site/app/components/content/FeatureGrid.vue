<script setup lang="ts">
defineProps<{ features: { icon: string, title: string, description: string }[] }>()
</script>

<template>
  <!-- The 1px gap over a border-coloured ground draws the dividers between cells. -->
  <div class="mx-auto grid w-full max-w-5xl gap-px text-left overflow-hidden rounded-2xl border border-default bg-(--ui-border) sm:grid-cols-2 lg:grid-cols-3">
    <div v-for="feature in features" :key="feature.title" class="bg-default p-8">
      <div class="mb-6 inline-flex size-10 items-center justify-center rounded-lg bg-primary/10">
        <UIcon :name="feature.icon" class="size-5 text-primary" />
      </div>
      <h3 class="mb-2 font-semibold text-highlighted">
        {{ feature.title }}
      </h3>
      <p class="text-muted">
        {{ feature.description }}
      </p>
    </div>
  </div>
</template>
