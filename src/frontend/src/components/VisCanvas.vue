<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { getFrame, player } from '../player/engine'
import type { CharId } from '../player/characters'
import { state } from '../store'
import Dancer from './Dancer.vue'
import { VIS_MODES, Visualizer, type VisMode } from '../player/visualizer'

const props = defineProps<{ active: boolean }>()

const canvas = ref<HTMLCanvasElement>()
const wrap = ref<HTMLDivElement>()
const char = computed(() => (state.settings?.character ?? 'miku') as CharId | 'none')
const choice = ref<VisMode | 'auto'>('auto')
const shown = ref<VisMode>('bars')

let vis: Visualizer | null = null
let raf = 0
let last = 0
let modeSince = 0
let ro: ResizeObserver | null = null

const AUTO_SECS = 20

function fit() {
  if (!vis || !wrap.value) return
  const r = wrap.value.getBoundingClientRect()
  if (r.width > 0 && r.height > 0) vis.resize(r.width, r.height, Math.min(2, window.devicePixelRatio || 1))
}

function loop(ts: number) {
  raf = requestAnimationFrame(loop)
  const now = ts / 1000
  const dt = Math.min(0.1, last ? now - last : 1 / 60)
  last = now
  if (choice.value === 'auto') {
    if (now - modeSince > AUTO_SECS) {
      const i = VIS_MODES.findIndex((m) => m.id === shown.value)
      shown.value = VIS_MODES[(i + 1) % VIS_MODES.length].id
      modeSince = now
    }
  } else {
    shown.value = choice.value
  }
  vis?.draw(shown.value, getFrame(now, dt))
}

function start() {
  if (raf || !vis) return
  last = 0
  raf = requestAnimationFrame(loop)
}
function stopLoop() {
  cancelAnimationFrame(raf)
  raf = 0
}

function pick(m: VisMode | 'auto') {
  choice.value = m
  modeSince = last
  if (m !== 'auto') shown.value = m
}

onMounted(() => {
  vis = new Visualizer(canvas.value!)
  ro = new ResizeObserver(fit)
  ro.observe(wrap.value!)
  fit()
  if (props.active) start()
  // Para pruebas: ?vis=mandala fija el modo.
  const q = new URLSearchParams(location.search).get('vis')
  if (q && VIS_MODES.some((m) => m.id === q)) pick(q as VisMode)
})
onBeforeUnmount(() => {
  stopLoop()
  ro?.disconnect()
})
watch(
  () => props.active,
  (a) => {
    if (a) {
      fit()
      start()
    } else stopLoop()
  },
)
</script>

<template>
  <div ref="wrap" class="viswrap">
    <canvas ref="canvas" />
    <Dancer v-if="char !== 'none'" :character="char" :playing="player.playing" :active="active" />
    <div class="visbar">
      <button class="vbtn" :class="{ on: choice === 'auto' }" @click="pick('auto')">AUTO</button>
      <button
        v-for="m in VIS_MODES"
        :key="m.id"
        class="vbtn"
        :class="{ on: choice === m.id }"
        @click="pick(m.id)"
      >
        {{ m.label }}
      </button>
    </div>
  </div>
</template>
