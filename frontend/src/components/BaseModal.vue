<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

defineProps<{ title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()

const card = ref<HTMLElement | null>(null)
const headingId = `modal-title-${Math.random().toString(36).slice(2, 10)}`
let previouslyFocused: HTMLElement | null = null

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function focusable(): HTMLElement[] {
  if (!card.value) return []
  return Array.from(card.value.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((element) => element.offsetParent !== null || element === document.activeElement)
}

// Keep Tab inside the dialog. The backdrop covers the whole viewport, so without this the
// focus ring walks onto page content the user cannot see and cannot get back from without
// reloading — the dialog was unusable by keyboard and screen reader.
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.stopPropagation()
    emit('close')
    return
  }
  if (event.key !== 'Tab') return
  const elements = focusable()
  if (!elements.length) return
  const first = elements[0]
  const last = elements[elements.length - 1]
  const active = document.activeElement as HTMLElement | null
  if (event.shiftKey && (active === first || !card.value?.contains(active))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(async () => {
  previouslyFocused = document.activeElement as HTMLElement | null
  await nextTick()
  const elements = focusable()
  ;(elements[0] || card.value)?.focus()
})

onBeforeUnmount(() => {
  // Return focus to whatever opened the dialog.
  previouslyFocused?.focus?.()
})
</script>

<template>
  <Teleport to="body">
    <div class="modal-backdrop" role="presentation" @click.self="emit('close')" @keydown="onKeydown">
      <section ref="card" class="modal-card" :class="{ wide }" role="dialog" aria-modal="true" :aria-labelledby="headingId" tabindex="-1">
        <header><h2 :id="headingId">{{ title }}</h2><button class="icon-button" aria-label="关闭" @click="emit('close')">×</button></header>
        <slot />
      </section>
    </div>
  </Teleport>
</template>

