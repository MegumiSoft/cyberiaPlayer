<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatDuration } from '../format'
import {
  current, cycleLoop, formatInfo, next, player, prev, seek, setShuffle, setVolume, stop, toggle,
} from '../player/engine'
import {
  addMemoryCue, deleteHotCue, deleteMemoryCue, edit, firstBeatHere, nudgeGrid, shiftDownbeat, pressHotCue, resetGrid, scaleGridBpm, setGridBpm,
} from '../player/edit'
import { CUE_COLORS, gridOf, HOT_LETTERS, metaOf, sortedCues } from '../player/trackmeta'
import { keyText } from '../player/analysis'
import { state } from '../store'
import EqPanel from './EqPanel.vue'
import LibraryPanel from './LibraryPanel.vue'
import NormalizeDialog from './NormalizeDialog.vue'
import VisCanvas from './VisCanvas.vue'
import WaveformPro from './WaveformPro.vue'

const props = defineProps<{ active: boolean }>()

const cur = computed(() => current())
const info = computed(() => {
  void player.duration
  void player.index
  return formatInfo()
})
const timeText = computed(() => {
  const t = Math.floor(player.time)
  return `${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`
})
const title = computed(() => {
  if (player.error) return player.error.toUpperCase()
  if (!cur.value) return 'CYBERIA LIBRARY MANAGEMENT SYSTEM'
  return cur.value.track.title
})
const keyLabel = computed(() => keyText(player.key, state.settings?.keyNotation ?? 'camelot'))
const keyConf = computed(() => Math.round((player.key?.conf ?? 0) * 100))
const keyBars = computed(() => Math.round((player.key?.conf ?? 0) * 5))
const hasWave = computed(() => !!player.analysis || player.analyzing)
const grid = computed(() => gridOf(cur.value?.track.path))
const meta = computed(() => (cur.value ? metaOf(cur.value.track.path) : null))
const normDlg = ref<InstanceType<typeof NormalizeDialog>>()
const hot = computed(() => Array.from({ length: 8 }, (_, i) => meta.value?.cues.find((c) => c.slot === i) ?? null))
const memory = computed(() => (meta.value ? sortedCues(meta.value).filter((c) => c.slot === -1) : []))
const fmtCue = (ms: number) => {
  const t = ms / 1000
  return `${Math.floor(t / 60)}:${String(Math.floor(t % 60)).padStart(2, '0')}.${String(Math.floor((t % 1) * 10))}`
}
function onBpm(e: Event) {
  const v = parseFloat((e.target as HTMLInputElement).value.replace(',', '.'))
  if (v >= 40 && v <= 300) setGridBpm(v)
  else (e.target as HTMLInputElement).value = grid.value ? grid.value.bpm.toFixed(2) : ''
}
function onHotClick(i: number, e: MouseEvent) {
  if (e.shiftKey || e.altKey) deleteHotCue(i)
  else pressHotCue(i)
  ;(e.currentTarget as HTMLElement | null)?.blur() // para que la barra espaciadora no vuelva a pulsar el botón
}
// Mientras se mantiene Shift los cues puestos se ven en rojo con una «×»: ahí el clic los borra.
const shiftDown = ref(false)
const onShift = (e: KeyboardEvent) => (shiftDown.value = e.shiftKey)
const onBlurAll = () => (shiftDown.value = false)
// Teclas 1-8: hot cues A-H (no se activan mientras se escribe en un campo)
function onKey(e: KeyboardEvent) {
  const tag = (e.target as HTMLElement | null)?.tagName
  if (!props.active || tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || e.ctrlKey || e.metaKey) return
  if (e.key === ' ' || e.code === 'Space') {
    e.preventDefault()
    ;(e.target as HTMLElement | null)?.blur?.()
    if (!e.repeat && player.queue.length) toggle()
    return
  }
  if (e.key >= '1' && e.key <= '8') pressHotCue(Number(e.key) - 1)
}
// El botón enfocado no debe activarse al soltar la barra espaciadora.
function onKeyUp(e: KeyboardEvent) {
  if (props.active && (e.key === ' ' || e.code === 'Space') && (e.target as HTMLElement | null)?.tagName === 'BUTTON') e.preventDefault()
}
onMounted(() => {
  window.addEventListener('keydown', onKey)
  window.addEventListener('keydown', onShift)
  window.addEventListener('keyup', onShift)
  window.addEventListener('keyup', onKeyUp)
  window.addEventListener('blur', onBlurAll)
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('keydown', onShift)
  window.removeEventListener('keyup', onShift)
  window.removeEventListener('keyup', onKeyUp)
  window.removeEventListener('blur', onBlurAll)
})
const loopText = computed(() => (player.loop === 'one' ? 'LOOP 1' : 'LOOP'))
const long = computed(() => title.value.length > 34)

function onSeek(e: Event) {
  seek(Number((e.target as HTMLInputElement).value))
}
function onVol(e: Event) {
  setVolume(Number((e.target as HTMLInputElement).value) / 100)
}
</script>

<template>
  <div class="player">
    <div class="pl-left">
      <VisCanvas :active="active" />

      <div class="panel-box">
        <div class="deck">
          <div class="lcd">
            <div class="time">{{ timeText }}</div>
            <div class="meta">
              <span><b>{{ info.kbps || '---' }}</b>KBPS</span>
              <span><b>{{ info.khz || '--' }}</b>KHZ</span>
            </div>
            <div class="bk">
              <div>
                <b>{{ player.bpm ? player.bpm.toFixed(1) : '---' }}</b>
                <small>BPM</small>
              </div>
              <div :title="player.key ? 'Confianza de la tonalidad: ' + keyConf + ' %' : ''">
                <b class="k">{{ keyLabel }}</b>
                <small>
                  KEY
                  <span class="conf" :class="{ off: !player.key }"><i v-for="n in 5" :key="n" :class="{ on: n <= keyBars }" /></span>
                </small>
              </div>
            </div>
          </div>
          <div style="min-width: 0">
            <div class="lcd">
              <div class="ticker">
                <span v-if="long" class="roll">{{ title }}</span>
                <span v-if="long" class="roll">{{ title }}</span>
                <span v-else>{{ title }}</span>
              </div>
              <div class="ticker-sub">
                {{ cur ? cur.playlist.toUpperCase() : 'NO TRACK' }}
                <template v-if="player.loading"> · <i class="spin" />CARGANDO{{ player.loadPct > 0 && player.loadPct < 100 ? ' ' + player.loadPct + '%' : '…' }}</template>
                <template v-else-if="cur"> · {{ formatDuration(player.duration) || '--:--' }}</template>
              </div>
            </div>
            <div v-if="player.loading" class="loadbar" :class="{ ind: !player.loadPct }">
              <i :style="{ width: player.loadPct + '%' }" />
            </div>
            <div class="seekrow">
              <input
                v-if="!hasWave"
                type="range"
                min="0"
                :max="Math.max(1, Math.floor(player.duration))"
                step="1"
                :value="Math.floor(player.time)"
                aria-label="Posición"
                @input="onSeek"
              />
              <div class="vol">
                VOL
                <input type="range" min="0" max="100" :value="Math.round(player.volume * 100)" aria-label="Volumen" @input="onVol" />
              </div>
            </div>
            <div class="transport">
              <button class="tbtn" title="Anterior" aria-label="Anterior" @click="prev">
                <svg viewBox="0 0 16 16"><path d="M3 2h2v12H3zM14 2v12L6 8z" /></svg>
              </button>
              <button class="tbtn play" :class="{ busy: player.loading, on: player.playing }" title="Play" aria-label="Play" :disabled="player.playing" @click="toggle">
                <svg viewBox="0 0 16 16"><path d="M3 1.5v13L14 8z" /></svg>
              </button>
              <button class="tbtn" :class="{ on: !player.playing && player.time > 0 }" title="Pausa" aria-label="Pausa" :disabled="!player.playing" @click="toggle">
                <svg viewBox="0 0 16 16"><path d="M3 2h4v12H3zM9 2h4v12H9z" /></svg>
              </button>
              <button class="tbtn" title="Stop" aria-label="Stop" @click="stop">
                <svg viewBox="0 0 16 16"><path d="M3 3h10v10H3z" /></svg>
              </button>
              <button class="tbtn" title="Siguiente" aria-label="Siguiente" @click="next(false)">
                <svg viewBox="0 0 16 16"><path d="M11 2h2v12h-2zM2 2l8 6-8 6z" /></svg>
              </button>
              <span class="sp" />
              <button class="tog m norm" :disabled="!cur" title="Igualar el volumen de esta canción con el de otra de referencia (sobrescribe el archivo)" @click="cur && normDlg?.open(cur.track)">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M2 6.5h2.5L8 3.5v9L4.5 9.5H2z" /><path d="M10.5 6a3 3 0 0 1 0 4M12.5 4.2a5.6 5.6 0 0 1 0 7.6" /></svg>
                NORMALIZAR
              </button>
              <button class="tbtn sw" :class="{ on: player.shuffle }" title="Shuffle (aleatorio)" aria-label="Shuffle" @click="setShuffle(!player.shuffle)">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M1.5 4.5h2.5c3 0 4 7 7 7h2.5M1.5 11.5h2.5c1.2 0 2-1 2.8-2.2M9.2 6.7C9.9 5.6 10.7 4.5 12 4.5h1.5" />
                  <path d="M12 2.5l2 2-2 2M12 9.5l2 2-2 2" />
                </svg>
              </button>
              <button class="tbtn sw" :class="{ on: player.loop !== 'off' }" :title="loopText" aria-label="Repetir" @click="cycleLoop">
                <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M2.5 7V6a2 2 0 0 1 2-2h8.5M13.5 9v1a2 2 0 0 1-2 2H3" />
                  <path d="M11 2l2.5 2L11 6M5 10l-2.5 2L5 14" />
                </svg>
                <b v-if="player.loop === 'one'" class="one">1</b>
              </button>
            </div>
          </div>
        </div>

        <div v-if="hasWave" class="wavestack">
          <WaveformPro mode="detail" :active="active" />
          <WaveformPro mode="overview" :active="active" />
          <div v-if="cur" class="editor">
            <div class="erow">
              <span class="elabel">REJILLA</span>
              <input
                class="bpmin"
                type="text"
                inputmode="decimal"
                :value="grid ? grid.bpm.toFixed(2) : ''"
                placeholder="BPM"
                aria-label="BPM de la rejilla"
                @change="onBpm"
              />
              <button class="tog m" :disabled="!grid" title="Mitad de tempo" @click="scaleGridBpm(0.5)">÷2</button>
              <button class="tog m" :disabled="!grid" title="Doble de tempo" @click="scaleGridBpm(2)">×2</button>
              <span class="nudge" :class="{ off: !grid }" title="Mover toda la rejilla (milisegundos)">
                <button class="tog m" :disabled="!grid" @click="nudgeGrid(-0.01)">−10</button>
                <button class="tog m" :disabled="!grid" @click="nudgeGrid(-0.001)">−1</button>
                <span class="hint">MS</span>
                <button class="tog m" :disabled="!grid" @click="nudgeGrid(0.001)">+1</button>
                <button class="tog m" :disabled="!grid" @click="nudgeGrid(0.01)">+10</button>
              </span>
              <button
                class="tog m on"
                :disabled="!grid"
                title="Fija AQUÍ el pulso 1 (inicio de compás/frase): toda la rejilla se acomoda a partir de este punto con el BPM actual. Pausa justo en el golpe fuerte; con «AJUSTAR AL GOLPE» se afina al ataque más cercano."
                @click="firstBeatHere"
              >
                PULSO 1 AQUÍ
              </button>
              <label class="check snap" title="Al fijar el pulso 1, mueve el punto al ataque (subida de graves) más cercano, hasta 90 ms"><input v-model="edit.onset" type="checkbox" /> AJUSTAR AL GOLPE</label>
              <span class="nudge" :class="{ off: !grid }" title="Cambia cuál es el pulso 1 sin mover la rejilla (si el golpe fuerte cae en el 2, 3 o 4)">
                <button class="tog m" :disabled="!grid" @click="shiftDownbeat(-1)">◂ 1</button>
                <span class="hint">FASE</span>
                <button class="tog m" :disabled="!grid" @click="shiftDownbeat(1)">1 ▸</button>
              </span>
              <button v-if="grid?.edited" class="tog m" @click="resetGrid">RESTAURAR</button>
              <span v-if="grid" class="hint">{{ grid.edited ? 'EDITADA' : 'DETECTADA' }}</span>
            </div>
            <div class="erow">
              <span class="elabel">HOT CUES</span>
              <button
                v-for="(c, i) in hot"
                :key="i"
                class="pad"
                :class="{ set: !!c, del: !!c && shiftDown }"
                :style="c ? { '--c': CUE_COLORS[c.color] } : undefined"
                :title="c ? `Saltar a ${fmtCue(c.timeMs)} (Shift+clic lo borra)` : 'Crear aquí (tecla ' + (i + 1) + ')'"
                @click="onHotClick(i, $event)"
                @contextmenu.prevent="deleteHotCue(i)"
              >
                <b>{{ HOT_LETTERS[i] }}</b>
                <small>{{ c ? (shiftDown ? '×' : fmtCue(c.timeMs)) : '—' }}</small>
              </button>
              <span class="hint">SHIFT+CLIC = BORRAR</span>
              <label class="check snap"><input v-model="edit.snap" type="checkbox" /> SNAP A LA REJILLA</label>
            </div>
            <div class="erow">
              <span class="elabel">MEMORY</span>
              <button class="tog m" @click="addMemoryCue">+ AQUÍ</button>
              <span v-for="m in memory" :key="m.timeMs" class="chip">
                <a @click="seek(m.timeMs / 1000)">{{ fmtCue(m.timeMs) }}</a>
                <i title="Borrar" @click="deleteMemoryCue(m.timeMs)">×</i>
              </span>
              <span class="hint">TECLAS 1-8 = HOT CUES · CLIC DERECHO BORRA</span>
            </div>
          </div>
        </div>
      </div>
    </div>

    <NormalizeDialog ref="normDlg" />

    <div class="pl-right">
      <EqPanel />
      <LibraryPanel />
    </div>
  </div>
</template>
