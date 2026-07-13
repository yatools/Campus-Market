<script setup lang="ts">
defineProps<{ title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <Teleport to="body">
    <div class="modal-backdrop" role="presentation" @click.self="emit('close')">
      <section class="modal-card" :class="{ wide }" role="dialog" aria-modal="true" :aria-label="title">
        <header><h2>{{ title }}</h2><button class="icon-button" aria-label="关闭" @click="emit('close')">×</button></header>
        <slot />
      </section>
    </div>
  </Teleport>
</template>

