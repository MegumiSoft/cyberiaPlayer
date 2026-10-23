<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { current, player, playbackTime, seek } from '../player/engine'
import { CUE_COLORS, gridOf, HOT_LETTERS, metaOf } from '../player/trackmeta'
import { drawBands } from '../player/wavedraw'

// Dos vistas de la misma forma de onda en 3 bandas:
//  - "overview": todo el tema, con cabezal; clic/arrastre para saltar.
//  - "detail":   ventana de unos segundos que se desplaza, con rejilla de pulsos y números de compás.
const props = defineProps<{ mode: 'overview' | 'detail'; active: boolean }>()
const WINDOW = 8 // segundos visibles en la vista de detalle

const wrap = ref<HTMLDivElement>()
const canvas = ref<HTMLCanvasElement>()
let raf = 0
let w = 1
let h = 1
let dpr = 1
let ro: ResizeObserver | null = null
let cache: HTMLCanvasElement | null = null
let cacheKey = ''
let dragging = false
let dragX = 0
let dragT = 0

function fit() {
  if (!wrap.value || !canvas.value) return
  const r = wrap.value.getBoundingClientRect()
  dpr = Math.min(2, window.devicePixelRatio || 1)
  w = Math.max(1, Math.floor(r.width * dpr))
  h = Math.max(1, Math.floor(r.height * dpr))
  canvas.value.width = w
  canvas.value.height = h
  cacheKey = ''
}

/** La vista general se pinta una sola vez en un lienzo aparte y luego solo se copia. */
function overviewLayer(): HTMLCanvasElement | null {
  const an = player.analysis
  if (!an) return null
  const key = `${an.frames}:${w}x${h}`
  if (cache && key === cacheKey) return cache
  cache = cache ?? document.createElement('canvas')
  cache.width = w
  cache.height = h
  const c = cache.getContext('2d')!
  c.clearRect(0, 0, w, h)
  const colW = Math.max(1, Math.round(dpr))
  drawBands(c, an, w, h, (x) => [Math.floor((x / w) * an.frames), Math.floor(((x + colW) / w) * an.frames)], colW)
  cacheKey = key
  return cache
}

function drawOverview(ctx: CanvasRenderingContext2D) {
  const an = player.analysis
  if (!an) return
  const layer = overviewLayer()
  if (layer) ctx.drawImage(layer, 0, 0)
  const dur = an.duration || player.duration || 1
  const px = Math.min(1, playbackTime() / dur) * w
  // lo que aún no suena se oscurece, así se ve el avance de un vistazo
  ctx.fillStyle = 'rgba(5,6,10,0.55)'
  ctx.fillRect(px, 0, w - px, h)
  ctx.fillStyle = '#fff'
  ctx.fillRect(Math.floor(px), 0, Math.max(1, dpr), h)
  const path = current()?.track.path
  if (path) {
    for (const c of metaOf(path).cues) {
      const x = Math.round((c.timeMs / 1000 / dur) * w)
      ctx.fillStyle = c.slot >= 0 ? CUE_COLORS[c.color] : '#ff2fb0'
      ctx.fillRect(x, 0, Math.max(1, dpr), c.slot >= 0 ? h : h * 0.3)
      ctx.beginPath()
      ctx.moveTo(x - 3 * dpr, 0)
      ctx.lineTo(x + 4 * dpr, 0)
      ctx.lineTo(x, 5 * dpr)
      ctx.fill()
    }
  }
}

function drawDetail(ctx: CanvasRenderingContext2D) {
  const an = player.analysis
  if (!an) return
  const t = playbackTime()
  const t0 = t - WINDOW / 2
  const secPerPx = WINDOW / w
  const colW = Math.max(1, Math.round(dpr))
  const topBar = 11 * dpr // franja superior con los números de compás
  drawBands(
    ctx,
    an,
    w,
    h,
    (x) => [Math.floor((t0 + x * secPerPx) * an.fps), Math.ceil((t0 + (x + colW) * secPerPx) * an.fps)],
    colW,
    topBar,
  )

  // rejilla de pulsos: la línea de cada compás (4 pulsos) es más marcada y lleva su número
  const path = current()?.track.path
  const grid = gridOf(path)
  if (grid) {
    const first = Math.floor((t0 - grid.offset) / grid.period)
    const last = Math.ceil((t0 + WINDOW - grid.offset) / grid.period)
    ctx.font = `${8 * dpr}px "Silkscreen", monospace`
    ctx.textBaseline = 'top'
    // Compases numerados desde el primer pulso 1 que cae dentro de la canción (puede haber pulsos antes del desfase).
    const kBar0 = 4 * Math.ceil(-grid.offset / grid.period / 4 - 1e-6)
    for (let k = first; k <= last; k++) {
      const bt = grid.offset + k * grid.period
      if (bt < -1e-6) continue
      const x = Math.round((bt - t0) / secPerPx)
      if (x < 0 || x > w) continue
      const bar = k % 4 === 0
      ctx.fillStyle = bar ? 'rgba(255,255,255,0.75)' : 'rgba(255,255,255,0.28)'
      ctx.fillRect(x, bar ? 0 : topBar, Math.max(1, dpr), h)
      if (bar) {
        ctx.fillStyle = '#ffe600'
        ctx.fillText(String((k - kBar0) / 4 + 1), x + 3 * dpr, 1 * dpr)
      }
    }
  }
  // cues: línea de color con su letra (hot) o una marca magenta (memory)
  if (path) {
    ctx.font = `${9 * dpr}px "Silkscreen", monospace`
    for (const c of metaOf(path).cues) {
      const x = Math.round((c.timeMs / 1000 - t0) / secPerPx)
      if (x < -20 * dpr || x > w + 20 * dpr) continue
      const col = c.slot >= 0 ? CUE_COLORS[c.color] : '#ff2fb0'
      ctx.fillStyle = col
      ctx.fillRect(x, 0, Math.max(2, dpr * 1.5), h)
      if (c.slot >= 0) {
        ctx.fillRect(x, h - 13 * dpr, 12 * dpr, 13 * dpr)
        ctx.fillStyle = '#000'
        ctx.textBaseline = 'middle'
        ctx.fillText(HOT_LETTERS[c.slot], x + 3 * dpr, h - 6.5 * dpr)
        ctx.textBaseline = 'top'
      }
    }
  }
  // cabezal fijo al centro
  ctx.fillStyle = '#fff'
  ctx.shadowColor = '#18e0ff'
  ctx.shadowBlur = 8 * dpr
  ctx.fillRect(Math.floor(w / 2), 0, Math.max(1, dpr), h)
  ctx.shadowBlur = 0
}

function draw() {
  raf = requestAnimationFrame(draw)
  const c = canvas.value
  if (!c) return
  const ctx = c.getContext('2d')!
  ctx.clearRect(0, 0, w, h)
  if (props.mode === 'overview') drawOverview(ctx)
  else drawDetail(ctx)
}

function xFrac(e: PointerEvent): number {
  const r = wrap.value!.getBoundingClientRect()
  return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width))
}
function onDown(e: PointerEvent) {
  const dur = player.analysis?.duration || player.duration
  if (!dur) return
  dragging = true
  ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
  if (props.mode === 'overview') seek(xFrac(e) * dur)
  else {
    dragX = e.clientX
    dragT = playbackTime()
  }
}
function onMove(e: PointerEvent) {
  if (!dragging) return
  const dur = player.analysis?.duration || player.duration
  if (!dur) return
  if (props.mode === 'overview') seek(xFrac(e) * dur)
  else {
    const r = wrap.value!.getBoundingClientRect()
    seek(dragT - ((e.clientX - dragX) / r.width) * WINDOW) // arrastrar la onda hacia la izquierda avanza
  }
}
const onUp = () => (dragging = false)

const start = () => {
  if (!raf) raf = requestAnimationFrame(draw)
}
const stopLoop = () => {
  cancelAnimationFrame(raf)
  raf = 0
}
onMounted(() => {
  ro = new ResizeObserver(fit)
  ro.observe(wrap.value!)
  fit()
  if (props.active) start()
})
onBeforeUnmount(() => {
  stopLoop()
  ro?.disconnect()
})
watch(
  () => props.active,
  (a) => (a ? (fit(), start()) : stopLoop()),
)
watch(() => player.analysis, () => (cacheKey = ''))
</script>

<template>
  <div
    ref="wrap"
    class="wavepro"
    :class="[mode, { off: !player.analysis }]"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @pointercancel="onUp"
  >
    <canvas ref="canvas" />
    <span v-if="!player.analysis" class="wave-note">{{ player.analyzing ? 'ANALIZANDO…' : 'SIN ANÁLISIS' }}</span>
  </div>
</template>
