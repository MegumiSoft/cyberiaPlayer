<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '../api'
import { errorText, formatDuration } from '../format'
import { analyzeAll, batch, isAnalyzed, pendingTracks } from '../player/libanalysis'
import { current, libraryToQueue, player, playIndex, prefetch, setQueue } from '../player/engine'
import { playlistName, moveTrackTo } from '../player/trackops'
import ConvertDialog from './ConvertDialog.vue'
import TrackDialogs from './TrackDialogs.vue'
import { guarded, loadLibrary, selectedPlaylist, state, toast, trackUrl } from '../store'
import type { LibPlaylist } from '../types'

const query = ref('')
const creating = ref(false)
const newName = ref('')
const newInput = ref<HTMLInputElement>()
const dropOver = ref('')

const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()
const q = computed(() => norm(query.value.trim()))

const folders = computed<LibPlaylist[]>(() => {
  if (!q.value) return state.library.playlists
  return state.library.playlists.filter(
    (p) => norm(p.name).includes(q.value) || p.tracks.some((t) => norm(t.title).includes(q.value)),
  )
})

const pl = selectedPlaylist
// Si el nombre de la carpeta coincide se ven todas sus canciones; si no, solo las que coinciden.
const tracks = computed(() => {
  const p = pl.value
  if (!p) return []
  if (!q.value || norm(p.name).includes(q.value)) return p.tracks
  return p.tracks.filter((t) => norm(t.title).includes(q.value))
})
const playingPath = computed(() => current()?.track.path ?? '')

// Al buscar, si la carpeta abierta ya no está en la lista se salta a la primera que coincide.
watch(folders, (list) => {
  if (list.length && !list.some((p) => p.id === pl.value?.id)) state.selected = list[0].id
})

function choose(p: LibPlaylist) {
  state.selected = p.id
}

function play(i: number) {
  const p = pl.value
  if (!p) return
  const t = tracks.value[i]
  const idx = p.tracks.findIndex((x) => x.path === t.path)
  // Si la lista ya está en la cola solo se salta a la pista; si no, se carga entera.
  if (player.queue[0]?.playlist === p.name && player.queue.length === p.tracks.length) playIndex(idx)
  else setQueue(libraryToQueue(p, trackUrl), idx)
}

function playAll() {
  if (!tracks.value.length) return
  play(0)
}

async function pickRoot() {
  const dir = await guarded(() => api.chooseFolder())
  if (!dir || !state.settings) return
  const saved = await guarded(() => api.saveSettings({ ...state.settings!, outDir: dir }))
  if (saved) state.settings = saved
  await loadLibrary()
}

async function startCreate() {
  creating.value = true
  newName.value = ''
  await nextTick()
  newInput.value?.focus()
}

async function create() {
  const name = newName.value.trim()
  if (!name) {
    creating.value = false
    return
  }
  try {
    const rel = await api.createPlaylist(name)
    creating.value = false
    query.value = ''
    await loadLibrary()
    const made = state.library.playlists.find((p) => p.path === rel)
    if (made) state.selected = made.id
    toast(`Playlist «${rel}» creada`)
  } catch (e) {
    toast(errorText(e))
  }
}

const pending = computed(() => (pl.value ? pendingTracks(pl.value.tracks).length : 0))
const unanalyzed = computed(() => (pl.value ? pl.value.tracks.filter((t) => !isAnalyzed(t)).length : 0))
async function analyzeList() {
  const p = pl.value
  if (!p || batch.running) return
  await analyzeAll(p.tracks)
  if (!batch.cancel) toast(`Análisis terminado${batch.failed ? ` (${batch.failed} no se pudieron analizar)` : ''}`)
}

const nameOf = playlistName

const move = (path: string, dest: LibPlaylist) => moveTrackTo(path, dest.path, dest.name)

// Al abrir una playlist se deja lista su primera canción: al pulsar play suena de inmediato.
let pf: ReturnType<typeof setTimeout> | undefined
watch(
  () => pl.value?.id,
  () => {
    clearTimeout(pf)
    pf = setTimeout(() => prefetch(pl.value?.tracks[0]), 400)
  },
)

const dlg = ref<InstanceType<typeof TrackDialogs>>()
const convDlg = ref<InstanceType<typeof ConvertDialog>>()
const openPicker = (t: { path: string; title: string }) => dlg.value?.openPicker({ ...t, from: nameOf(pl.value ?? undefined) })
const askTrash = (t: { path: string; title: string }) => dlg.value?.askTrash({ ...t, from: nameOf(pl.value ?? undefined) })

function onDragStart(e: DragEvent, path: string) {
  e.dataTransfer?.setData('text/plain', path)
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

function onDrop(e: DragEvent, dest: LibPlaylist) {
  dropOver.value = ''
  const path = e.dataTransfer?.getData('text/plain')
  if (path) void move(path, dest)
}

</script>

<template>
  <div class="panel-box lib">
    <div class="cap">Library</div>
    <div class="rootpath" :title="state.library.root">{{ state.library.root || 'SIN CARPETA' }}</div>

    <div class="row" style="margin-bottom: 8px; flex-wrap: nowrap">
      <input v-model="query" class="grow search" type="text" placeholder="Buscar carpeta o canción…" spellcheck="false" />
      <button class="btn small" title="Crear una playlist (carpeta) nueva" @click="startCreate">+ Nueva</button>
    </div>
    <div v-if="creating" class="row" style="margin-bottom: 8px; flex-wrap: nowrap">
      <input
        ref="newInput"
        v-model="newName"
        class="grow"
        type="text"
        placeholder="Nombre de la playlist"
        spellcheck="false"
        @keydown.enter="create"
        @keydown.esc="creating = false"
      />
      <button class="btn small primary" @click="create">Crear</button>
      <button class="btn small" @click="creating = false">✕</button>
    </div>

    <div v-if="!state.library.playlists.length" class="empty">
      {{ state.libraryLoading ? 'Leyendo biblioteca…' : 'No hay música en esta carpeta todavía.' }}<br />
      Descarga algo en <b>FETCH</b>, crea una playlist o elige otra carpeta.
    </div>
    <template v-else>
      <div class="folders">
        <button
          v-for="p in folders"
          :key="p.id"
          class="folder"
          :class="{ on: pl?.id === p.id, drop: dropOver === p.id }"
          :title="p.name"
          @click="choose(p)"
          @dragover.prevent="dropOver = p.id"
          @dragleave="dropOver = ''"
          @drop.prevent="onDrop($event, p)"
        >
          {{ p.name }}<span class="c">{{ p.tracks.length }}</span>
        </button>
        <div v-if="!folders.length" class="hint">SIN COINCIDENCIAS</div>
      </div>
      <ul class="tracks">
        <li
          v-for="(t, i) in tracks"
          :key="t.id"
          class="trk"
          :class="{ cur: playingPath === t.path, todo: !isAnalyzed(t) }"
          draggable="true"
          @dragstart="onDragStart($event, t.path)"
          @click="play(i)"
        >
          <span class="i">{{ i + 1 }}.</span>
          <span class="t">{{ t.title }}</span>
          <button class="mv" title="Mover a otra playlist" @click.stop="openPicker(t)">MOVER</button>
          <button class="mv del" title="Enviar a la papelera" @click.stop="askTrash(t)">BORRAR</button>
          <span class="d">{{ formatDuration(state.durations[t.path]) || '--:--' }}</span>
        </li>
        <li v-if="!tracks.length" class="empty" style="padding: 18px 8px">
          {{ pl?.tracks.length ? 'Ninguna canción coincide.' : 'Playlist vacía: arrastra canciones aquí desde otra carpeta o descarga a esta.' }}
        </li>
      </ul>
    </template>

    <div v-if="state.library.playlists.length" class="analyzebar">
      <button v-if="!batch.running" class="btn small primary" :disabled="!pending" :title="unanalyzed ? 'Analiza una por una las canciones de esta playlist que aún no tienen análisis' : 'Todas las canciones de esta playlist ya están analizadas'" @click="analyzeList">
        {{ pending ? `ANALIZAR TODAS (${pending})` : 'TODAS ANALIZADAS' }}
      </button>
      <button v-else class="btn small danger" @click="batch.cancel = true">DETENER ({{ batch.done }}/{{ batch.total }})</button>
      <button v-if="!batch.running" class="btn small" :disabled="!pl || !pl.tracks.length" title="Convertir toda esta playlist a otro formato (p. ej. WAV → MP3)" @click="pl && convDlg?.open(pl)">CONVERTIR…</button>
      <span class="hint legend"><i class="lg y" />SIN ANALIZAR <i class="lg g" />ANALIZADA <i class="lg w" />SONANDO</span>
      <div v-if="batch.running" class="loadbar"><i :style="{ width: (batch.total ? (batch.done / batch.total) * 100 : 0) + '%' }" /></div>
      <div v-if="batch.running" class="hint">ANALIZANDO: {{ batch.current }}</div>
    </div>

    <div class="libfoot">
      <button class="btn" :disabled="!tracks.length" @click="playAll">▶ Play all</button>
      <button class="btn" @click="loadLibrary">Refresh</button>
      <button class="btn" @click="pickRoot">Folder…</button>
      <button class="btn" @click="guarded(() => api.openDownloads())">Open</button>
    </div>

    <TrackDialogs ref="dlg" />
    <ConvertDialog ref="convDlg" />
  </div>
</template>
