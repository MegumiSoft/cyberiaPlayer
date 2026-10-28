<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { errorText, formatDuration } from '../format'
import { keyText } from '../player/analysis'
import { current, libraryToQueue, player, playIndex, setQueue } from '../player/engine'
import { compatScore, RELATION_LABEL, wheel, type Relation } from '../player/harmony'
import { analyzeAll, batch, pendingTracks } from '../player/libanalysis'
import { pal, rgba } from '../player/palette'
import { gridOf, loadMetas, metas } from '../player/trackmeta'
import { playlistName } from '../player/trackops'
import { loadLibrary, state, toast, trackUrl } from '../store'
import type { LibPlaylist, LibTrack } from '../types'
import TrackDialogs from './TrackDialogs.vue'

const props = defineProps<{ active: boolean }>()

interface Item {
  track: LibTrack
  pl: LibPlaylist
  bpm: number
  key: { pc: number; minor: boolean; conf: number } | null
}

const filterPl = ref('')
const tolerance = ref(6)
const selected = ref('')
const hover = ref<Item | null>(null)
const hoverPos = ref({ x: 0, y: 0 })
const dlg = ref<InstanceType<typeof TrackDialogs>>()
const notation = computed(() => state.settings?.keyNotation ?? 'camelot')

const allTracks = computed(() => state.library.playlists.flatMap((p) => p.tracks))
const pending = computed(() => pendingTracks(allTracks.value, state.durations).length)

const items = computed<Item[]>(() => {
  void metas // dependencia reactiva: se recalcula al terminar cada análisis
  const out: Item[] = []
  for (const pl of state.library.playlists) {
    if (filterPl.value && pl.id !== filterPl.value) continue
    for (const track of pl.tracks) {
      const m = metas[track.path]
      const a = m?.analysis
      if (!a) continue
      const g = gridOf(track.path)
      out.push({ track, pl, bpm: g?.bpm ?? a.bpm, key: a.hasKey ? { pc: a.keyPc, minor: a.keyMinor, conf: a.keyConf } : null })
    }
  }
  return out
})
const plottable = computed(() => items.value.filter((i) => i.key && i.bpm > 0))
const sel = computed(() => plottable.value.find((i) => i.track.path === selected.value) ?? null)

interface Rec {
  item: Item
  rel: Relation
  pct: number
  factor: 1 | 2 | 0.5
  score: number
}
const recs = computed<Rec[]>(() => {
  const s = sel.value
  if (!s) return []
  const out: Rec[] = []
  for (const it of plottable.value) {
    if (it.track.path === s.track.path) continue
    const c = compatScore(s, it, tolerance.value)
    if (c.score > 0) out.push({ item: it, rel: c.rel, pct: c.pct, factor: c.factor, score: c.score })
  }
  return out.sort((a, b) => b.score - a.score).slice(0, 60)
})
const recPaths = computed(() => new Set(recs.value.map((r) => r.item.track.path)))

const kt = (it: Item) => keyText(it.key as never, notation.value)

// ------------------------------------------------------------------ gráfico
const wrap = ref<HTMLDivElement>()
const canvas = ref<HTMLCanvasElement>()
let W = 1
let H = 1
let dpr = 1
let ro: ResizeObserver | null = null
const M = { l: 40, r: 14, t: 10, b: 26 }

const range = computed(() => {
  const b = plottable.value.map((i) => i.bpm)
  if (!b.length) return { lo: 60, hi: 180 }
  let lo = Math.floor(Math.min(...b) / 5) * 5 - 5
  let hi = Math.ceil(Math.max(...b) / 5) * 5 + 5
  if (hi - lo < 30) {
    const mid = (hi + lo) / 2
    lo = mid - 15
    hi = mid + 15
  }
  return { lo, hi }
})

function jitter(id: string, k: number): number {
  let h = 2166136261 + k
  for (let i = 0; i < id.length; i++) h = Math.imul(h ^ id.charCodeAt(i), 16777619)
  return ((h >>> 0) % 1000) / 1000 - 0.5
}

function pos(it: Item): { x: number; y: number } {
  const w = wheel(it.key as never)
  const pw = W - M.l - M.r
  const ph = H - M.t - M.b
  const rowH = ph / 12
  const x = M.l + ((it.bpm - range.value.lo) / (range.value.hi - range.value.lo)) * pw + jitter(it.track.id, 1) * 5 * dpr
  const cy = M.t + (12 - w.n + 0.5) * rowH // la 12 arriba, la 1 abajo
  const y = cy + (w.letter === 'A' ? 0.2 : -0.2) * rowH + jitter(it.track.id, 2) * rowH * 0.22
  return { x, y }
}

function draw() {
  const c = canvas.value
  if (!c) return
  const ctx = c.getContext('2d')!
  ctx.clearRect(0, 0, W, H)
  const pw = W - M.l - M.r
  const ph = H - M.t - M.b
  const rowH = ph / 12
  ctx.font = `${8 * dpr}px "Silkscreen", monospace`
  ctx.textBaseline = 'middle'
  // filas de la rueda
  for (let n = 1; n <= 12; n++) {
    const y = M.t + (12 - n) * rowH
    ctx.fillStyle = n % 2 ? 'rgba(255,255,255,0.025)' : 'rgba(255,255,255,0)'
    ctx.fillRect(M.l, y, pw, rowH)
    ctx.fillStyle = rgba(pal((n - 1) / 11), 0.9)
    ctx.textAlign = 'right'
    ctx.fillText(String(n), M.l - 8 * dpr, y + rowH / 2)
  }
  // BPM
  ctx.textAlign = 'center'
  ctx.textBaseline = 'top'
  const step = range.value.hi - range.value.lo > 80 ? 10 : 5
  for (let b = Math.ceil(range.value.lo / step) * step; b <= range.value.hi; b += step) {
    const x = M.l + ((b - range.value.lo) / (range.value.hi - range.value.lo)) * pw
    ctx.fillStyle = 'rgba(255,255,255,0.06)'
    ctx.fillRect(x, M.t, Math.max(1, dpr * 0.6), ph)
    ctx.fillStyle = 'rgba(150,150,170,0.9)'
    ctx.fillText(String(b), x, H - M.b + 6 * dpr)
  }
  const s = sel.value
  const hasSel = !!s
  const drawDot = (it: Item, r: number, alpha: number, glow = false) => {
    const { x, y } = pos(it)
    const w = wheel(it.key as never)
    const col = pal((w.n - 1) / 11)
    ctx.fillStyle = rgba(col, alpha)
    if (glow) {
      ctx.shadowColor = rgba(col, 1)
      ctx.shadowBlur = 10 * dpr
    }
    ctx.beginPath()
    if (w.letter === 'A') ctx.arc(x, y, r, 0, Math.PI * 2)
    else {
      ctx.moveTo(x, y - r * 1.2)
      ctx.lineTo(x + r * 1.2, y)
      ctx.lineTo(x, y + r * 1.2)
      ctx.lineTo(x - r * 1.2, y)
      ctx.closePath()
    }
    ctx.fill()
    ctx.shadowBlur = 0
  }
  const playing = current()?.track.path
  for (const it of plottable.value) {
    if (hasSel && (it.track.path === s!.track.path || recPaths.value.has(it.track.path))) continue
    drawDot(it, 4 * dpr, hasSel ? 0.18 : 0.85)
  }
  for (const it of plottable.value) if (hasSel && recPaths.value.has(it.track.path)) drawDot(it, 5.5 * dpr, 1, true)
  if (s) {
    drawDot(s, 6 * dpr, 1, true)
    const p = pos(s)
    ctx.strokeStyle = '#fff'
    ctx.lineWidth = 2 * dpr
    ctx.beginPath()
    ctx.arc(p.x, p.y, 11 * dpr, 0, Math.PI * 2)
    ctx.stroke()
    // banda de tolerancia de BPM alrededor de la canción elegida
    const lo = M.l + ((s.bpm * (1 - tolerance.value / 100) - range.value.lo) / (range.value.hi - range.value.lo)) * pw
    const hi = M.l + ((s.bpm * (1 + tolerance.value / 100) - range.value.lo) / (range.value.hi - range.value.lo)) * pw
    ctx.fillStyle = 'rgba(24,224,255,0.07)'
    ctx.fillRect(lo, M.t, hi - lo, ph)
  }
  if (playing) {
    const it = plottable.value.find((i) => i.track.path === playing)
    if (it) {
      const p = pos(it)
      ctx.strokeStyle = '#ffe600'
      ctx.lineWidth = 1.5 * dpr
      ctx.strokeRect(p.x - 8 * dpr, p.y - 8 * dpr, 16 * dpr, 16 * dpr)
    }
  }
}

let raf = 0
function schedule() {
  if (raf) return
  raf = requestAnimationFrame(() => {
    raf = 0
    draw()
  })
}

function fit() {
  if (!wrap.value || !canvas.value) return
  const r = wrap.value.getBoundingClientRect()
  dpr = Math.min(2, window.devicePixelRatio || 1)
  W = Math.max(1, Math.floor(r.width * dpr))
  H = Math.max(1, Math.floor(r.height * dpr))
  canvas.value.width = W
  canvas.value.height = H
  M.l = 40 * dpr
  M.r = 14 * dpr
  M.t = 10 * dpr
  M.b = 26 * dpr
  schedule()
}

function nearest(e: MouseEvent): Item | null {
  const r = canvas.value!.getBoundingClientRect()
  const mx = (e.clientX - r.left) * dpr
  const my = (e.clientY - r.top) * dpr
  let best: Item | null = null
  let bd = (11 * dpr) ** 2
  for (const it of plottable.value) {
    const p = pos(it)
    const d = (p.x - mx) ** 2 + (p.y - my) ** 2
    if (d < bd) {
      bd = d
      best = it
    }
  }
  return best
}
function onMove(e: MouseEvent) {
  const it = nearest(e)
  hover.value = it
  const r = wrap.value!.getBoundingClientRect()
  hoverPos.value = { x: e.clientX - r.left + 12, y: e.clientY - r.top + 12 }
}
const onClick = (e: MouseEvent) => {
  const it = nearest(e)
  selected.value = it ? it.track.path : ''
}
const onDbl = (e: MouseEvent) => {
  const it = nearest(e)
  if (it) play(it)
}

watch([plottable, sel, recs, tolerance, () => current()?.track.path], schedule)
watch(
  () => props.active,
  (a) => a && nextTick(fit),
)
onMounted(() => {
  ro = new ResizeObserver(fit)
  ro.observe(wrap.value!)
  fit()
})
onBeforeUnmount(() => {
  ro?.disconnect()
  cancelAnimationFrame(raf)
})

// ------------------------------------------------------------------ acciones
function play(it: Item) {
  const idx = it.pl.tracks.findIndex((t) => t.path === it.track.path)
  if (player.queue[0]?.playlist === it.pl.name && player.queue.length === it.pl.tracks.length) playIndex(idx)
  else setQueue(libraryToQueue(it.pl, trackUrl), idx)
}
const move = (it: Item) => dlg.value?.openPicker({ path: it.track.path, title: it.track.title, from: playlistName(it.pl) })
const trash = (it: Item) => dlg.value?.askTrash({ path: it.track.path, title: it.track.title, from: playlistName(it.pl) })

async function startBatch() {
  if (!allTracks.value.length) return
  await analyzeAll(allTracks.value, state.durations)
  if (!batch.cancel) toast(`Análisis terminado${batch.failed ? ` (${batch.failed} no se pudieron analizar)` : ''}`)
}

// ------------------------------------------------------------------ set (lista de mezcla)
const SET_KEY = 'cyberia.set'
const setList = ref<string[]>([])
try {
  setList.value = JSON.parse(localStorage.getItem(SET_KEY) ?? '[]')
} catch {
  /* sin almacenamiento */
}
watch(
  setList,
  (v) => {
    try {
      localStorage.setItem(SET_KEY, JSON.stringify(v))
    } catch {
      /* ignorar */
    }
  },
  { deep: true },
)
const trackByPath = computed(() => {
  const m = new Map<string, Item>()
  for (const pl of state.library.playlists)
    for (const track of pl.tracks) {
      const a = metas[track.path]?.analysis
      const g = gridOf(track.path)
      m.set(track.path, { track, pl, bpm: g?.bpm ?? a?.bpm ?? 0, key: a?.hasKey ? { pc: a.keyPc, minor: a.keyMinor, conf: a.keyConf } : null })
    }
  return m
})
const setItems = computed(() => setList.value.map((p) => trackByPath.value.get(p)).filter((x): x is Item => !!x))
const addToSet = (it: Item) => !setList.value.includes(it.track.path) && setList.value.push(it.track.path)
const removeFromSet = (i: number) => setList.value.splice(i, 1)
function moveInSet(i: number, d: number) {
  const j = i + d
  if (j < 0 || j >= setList.value.length) return
  ;[setList.value[i], setList.value[j]] = [setList.value[j], setList.value[i]]
}
function transition(i: number): string {
  if (i === 0) return ''
  const a = setItems.value[i - 1]
  const b = setItems.value[i]
  if (!a?.key || !b?.key) return ''
  const c = compatScore(a, b, 100)
  return c.rel ? `${RELATION_LABEL[c.rel]} · ${c.pct > 0 ? '+' : ''}${c.pct.toFixed(1)} %` : 'CHOCA'
}
// ---- crear una playlist (carpeta) con el set actual ----
const savingSet = ref(false)
const setName = ref('')
const setBusy = ref(false)
const setInput = ref<HTMLInputElement>()
async function askSetName() {
  const d = new Date()
  setName.value = `Set ${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  savingSet.value = true
  await nextTick()
  setInput.value?.select()
}
async function saveSetAsPlaylist() {
  const name = setName.value.trim()
  if (!name || setBusy.value || !setItems.value.length) return
  setBusy.value = true
  try {
    const res = await api.createPlaylistFromSet(name, setItems.value.map((it) => it.track.path))
    savingSet.value = false
    // El backend copió rejilla y cues a las canciones nuevas: hay que recargar también la caché de metadatos, o al
    // reproducir una del set se guardaría un registro en blanco encima de lo copiado.
    await Promise.all([loadLibrary(), loadMetas()])
    const skipped = res.skipped?.length ?? 0
    toast(`Playlist «${res.folder}» creada con ${res.copied} canciones${skipped ? ` (${skipped} no se pudieron copiar)` : ''}`, 7000)
  } catch (e) {
    toast(errorText(e))
  } finally {
    setBusy.value = false
  }
}

function playSet() {
  const its = setItems.value
  if (!its.length) return
  setQueue(its.map((it) => ({ track: it.track, playlist: 'SET', url: trackUrl(it.track) })), 0)
}
</script>

<template>
  <div class="mappage">
    <section class="panel-box mapbox">
      <div class="cap">Map</div>
      <div class="maptop">
        <select v-model="filterPl" aria-label="Playlist">
          <option value="">TODA LA BIBLIOTECA</option>
          <option v-for="p in state.library.playlists" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
        <label class="tol">
          BPM ± {{ tolerance }} %
          <input v-model.number="tolerance" type="range" min="1" max="12" step="1" />
        </label>
        <button v-if="!batch.running" class="btn small primary" :disabled="!pending" @click="startBatch">
          {{ pending ? `Analizar ${pending} canciones` : 'Todo analizado' }}
        </button>
        <button v-else class="btn small danger" @click="batch.cancel = true">Detener ({{ batch.done }}/{{ batch.total }})</button>
        <button class="btn small" @click="loadLibrary">Refresh</button>
      </div>
      <div v-if="batch.running" class="loadbar"><i :style="{ width: (batch.total ? (batch.done / batch.total) * 100 : 0) + '%' }" /></div>
      <div v-if="batch.running" class="hint">ANALIZANDO: {{ batch.current }}</div>
      <div class="hint legend">
        EJE X = BPM · EJE Y = RUEDA CAMELOT · ● = A (MENOR) · ◆ = B (MAYOR) · CLIC = ELEGIR · DOBLE CLIC = REPRODUCIR · {{ plottable.length }} / {{ allTracks.length }} EN EL MAPA
      </div>
      <div ref="wrap" class="mapcanvas" @mousemove="onMove" @mouseleave="hover = null" @click="onClick" @dblclick="onDbl">
        <canvas ref="canvas" />
        <div v-if="hover" class="maptip" :style="{ left: hoverPos.x + 'px', top: hoverPos.y + 'px' }">
          <b>{{ hover.track.title }}</b>
          <span>{{ hover.pl.name }}</span>
          <span>{{ hover.bpm.toFixed(1) }} BPM · {{ kt(hover) }}</span>
        </div>
        <div v-if="!plottable.length" class="mapempty">
          {{ allTracks.length ? 'Aún no hay canciones analizadas: pulsa «Analizar».' : 'La biblioteca está vacía.' }}
        </div>
      </div>
    </section>

    <section class="panel-box maprecs">
      <div class="cap">{{ sel ? 'Recomendadas' : 'Recomendaciones' }}</div>
      <template v-if="sel">
        <div class="selcard">
          <div class="dlg-title">{{ sel.track.title }}</div>
          <div class="hint">{{ sel.pl.name.toUpperCase() }} · {{ sel.bpm.toFixed(1) }} BPM · {{ kt(sel) }}</div>
          <div class="row" style="margin-top: 6px">
            <button class="btn small" @click="play(sel)">▶ Play</button>
            <button class="btn small" @click="addToSet(sel)">+ Set</button>
            <button class="btn small" @click="move(sel)">Mover</button>
            <button class="btn small danger" @click="trash(sel)">Borrar</button>
          </div>
        </div>
        <ul class="reclist">
          <li v-for="r in recs" :key="r.item.track.id" class="rec" @click="selected = r.item.track.path" @dblclick="play(r.item)">
            <div class="rt">
              <span class="t">{{ r.item.track.title }}</span>
              <span class="d">{{ formatDuration(state.durations[r.item.track.path] || r.item.track.size && metas[r.item.track.path]?.analysis?.duration || 0) || '' }}</span>
            </div>
            <div class="rs">
              <span class="k" :style="{ color: rgba(pal((wheel(r.item.key as never).n - 1) / 11), 1) }">{{ kt(r.item) }}</span>
              <span>{{ r.item.bpm.toFixed(1) }} BPM</span>
              <span>{{ r.pct > 0 ? '+' : '' }}{{ r.pct.toFixed(1) }} %{{ r.factor !== 1 ? (r.factor === 2 ? ' (×2)' : ' (÷2)') : '' }}</span>
              <span class="rel">{{ r.rel ? RELATION_LABEL[r.rel] : '' }}</span>
              <span class="pln">{{ r.item.pl.name }}</span>
            </div>
            <div class="ra">
              <button class="mv" @click.stop="play(r.item)">PLAY</button>
              <button class="mv" @click.stop="addToSet(r.item)">+ SET</button>
              <button class="mv" @click.stop="move(r.item)">MOVER</button>
              <button class="mv del" @click.stop="trash(r.item)">BORRAR</button>
            </div>
          </li>
          <li v-if="!recs.length" class="empty">Nada compatible con ±{{ tolerance }} % de BPM. Sube la tolerancia o analiza más canciones.</li>
        </ul>
      </template>
      <div v-else class="empty">Elige una canción en el mapa para ver qué mezcla bien con ella: misma tonalidad, ±1 en la rueda, relativa (A↔B) o ±2 (cambio de energía), con BPM parecido.</div>

      <div class="cap" style="margin-top: 14px">Set</div>
      <ul class="setlist">
        <li v-for="(it, i) in setItems" :key="it.track.path">
          <div class="rt">
            <span class="i">{{ i + 1 }}.</span>
            <span class="t" @click="selected = it.track.path">{{ it.track.title }}</span>
            <span class="k">{{ it.key ? kt(it) : '--' }} · {{ it.bpm ? it.bpm.toFixed(0) : '---' }}</span>
            <button class="mv" @click="moveInSet(i, -1)">↑</button>
            <button class="mv" @click="moveInSet(i, 1)">↓</button>
            <button class="mv del" @click="removeFromSet(i)">✕</button>
          </div>
          <div v-if="transition(i)" class="hint tr">{{ transition(i) }}</div>
        </li>
        <li v-if="!setItems.length" class="empty">Añade canciones con «+ Set» para ordenar una mezcla.</li>
      </ul>
      <div class="row" style="margin-top: 8px">
        <button class="btn small primary" :disabled="!setItems.length" @click="playSet">▶ Reproducir set</button>
        <button class="btn small" :disabled="!setItems.length" title="Crea una playlist (carpeta) nueva con copias de las canciones del set, en este orden" @click="askSetName">Crear playlist…</button>
        <button class="btn small" :disabled="!setItems.length" @click="setList = []">Vaciar</button>
      </div>
    </section>
    <TrackDialogs ref="dlg" />
    <Teleport to="body">
      <div v-if="savingSet" class="modal" @mousedown.self="!setBusy && (savingSet = false)">
        <div class="dialog" role="dialog" aria-label="Crear playlist desde el set">
          <div class="cap">Crear playlist desde el set</div>
          <div class="dlg-title">{{ setItems.length }} canciones</div>
          <label class="label" for="setname">Nombre de la playlist</label>
          <input id="setname" ref="setInput" v-model="setName" type="text" maxlength="100" spellcheck="false" :disabled="setBusy" @keydown.enter="saveSetAsPlaylist" @keydown.esc="!setBusy && (savingSet = false)" />
          <div class="note">Se crea una carpeta nueva en la biblioteca con <b>copias</b> de estas canciones, numeradas en el orden del set (01, 02…). Las originales no se tocan.</div>
          <div class="row spread" style="margin-top: 12px">
            <span class="hint">{{ setBusy ? 'COPIANDO…' : 'ENTER CREAR · ESC CANCELAR' }}</span>
            <span class="row">
              <button class="btn small" :disabled="setBusy" @click="savingSet = false">Cancelar</button>
              <button class="btn small primary" :disabled="!setName.trim() || setBusy" @click="saveSetAsPlaylist">Crear playlist</button>
            </span>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
