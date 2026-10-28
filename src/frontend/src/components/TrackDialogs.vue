<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../format'
import { current, next, player, stop } from '../player/engine'
import { copyTrackTo, moveTrackTo } from '../player/trackops'
import { metas } from '../player/trackmeta'
import { loadLibrary, state, toast } from '../store'
import type { LibPlaylist } from '../types'

// Diálogos compartidos para mover una canción a otra playlist (con buscador) y mandarla a la papelera.
// Los usan la biblioteca del reproductor y el mapa de canciones.
const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()
const trashing = ref<{ path: string; title: string; from: string } | null>(null)
const cancelBtn = ref<HTMLButtonElement>()
// Por defecto se MUEVE; marcando la casilla se COPIA (la canción queda en las dos playlists).
const copyMode = ref(false)
const move = (path: string, dest: LibPlaylist) => (copyMode.value ? copyTrackTo(path, dest.path, dest.name) : moveTrackTo(path, dest.path, dest.name))

// ---- selector de destino con búsqueda ----
const picking = ref<{ path: string; title: string; from: string } | null>(null)
const pickQuery = ref('')
const pickIdx = ref(0)
const pickInput = ref<HTMLInputElement>()

const pickResults = computed(() => {
  const cur = picking.value
  if (!cur) return []
  const qq = norm(pickQuery.value.trim())
  const list = state.library.playlists.filter((p) => p.name !== cur.from)
  if (!qq) return list.slice(0, 80)
  const starts: LibPlaylist[] = []
  const has: LibPlaylist[] = []
  for (const p of list) {
    const n = norm(p.name)
    if (n.startsWith(qq)) starts.push(p)
    else if (n.includes(qq)) has.push(p)
  }
  return [...starts, ...has].slice(0, 80)
})
const canCreate = computed(() => {
  const n = pickQuery.value.trim()
  return !!n && !state.library.playlists.some((p) => norm(p.name) === norm(n))
})
watch(pickQuery, () => (pickIdx.value = 0))

async function openPicker(t: { path: string; title: string; from: string }) {
  picking.value = { path: t.path, title: t.title, from: t.from }
  pickQuery.value = ''
  pickIdx.value = 0
  copyMode.value = false
  await nextTick()
  pickInput.value?.focus()
}
const closePicker = () => (picking.value = null)

async function choosePick(i: number) {
  const cur = picking.value
  if (!cur) return
  const r = pickResults.value[i]
  if (r) {
    closePicker()
    await move(cur.path, r)
  } else if (canCreate.value) {
    const name = pickQuery.value.trim()
    closePicker()
    try {
      const rel = await api.createPlaylist(name)
      await (copyMode.value ? copyTrackTo(cur.path, rel, rel) : moveTrackTo(cur.path, rel, rel))
    } catch (e) {
      toast(errorText(e))
    }
  }
}

function onPickKey(e: KeyboardEvent) {
  const n = pickResults.value.length + (canCreate.value ? 1 : 0)
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    pickIdx.value = Math.min(n - 1, pickIdx.value + 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    pickIdx.value = Math.max(0, pickIdx.value - 1)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    void choosePick(pickIdx.value)
  } else if (e.key === 'Escape') {
    closePicker()
  }
}

async function askTrash(t: { path: string; title: string; from: string }) {
  trashing.value = { path: t.path, title: t.title, from: t.from }
  await nextTick()
  cancelBtn.value?.focus()
}

async function doTrash() {
  const t = trashing.value
  if (!t) return
  trashing.value = null
  // Si es la que suena, se pasa a otra (o se detiene) antes de soltar el archivo.
  if (current()?.track.path === t.path) {
    if (player.queue.length > 1) next(false)
    else stop()
    await new Promise((r) => setTimeout(r, 150))
  }
  try {
    await api.trashTrack(t.path)
    delete metas[t.path]
    toast(`«${t.title}» fue enviada a la papelera`, 6000)
    await loadLibrary()
  } catch (e) {
    toast(errorText(e))
  }
}


defineExpose({ openPicker, askTrash })
</script>

<template>
  <Teleport to="body">
      <div v-if="trashing" class="modal" @mousedown.self="trashing = null" @keydown.esc="trashing = null">
        <div class="dialog danger" role="alertdialog" aria-label="Enviar a la papelera">
          <div class="cap">Enviar a la papelera</div>
          <div class="dlg-title">{{ trashing.title }}</div>
          <div class="hint" style="margin-bottom: 10px">EN «{{ trashing.from.toUpperCase() }}»</div>
          <div class="sub">La canción irá a la papelera del sistema; desde ahí se puede recuperar.</div>
          <div class="row" style="margin-top: 14px; justify-content: flex-end">
            <button ref="cancelBtn" class="btn" @click="trashing = null">Cancelar</button>
            <button class="btn danger-fill" @click="doTrash">Enviar a la papelera</button>
          </div>
        </div>
      </div>
      <div v-if="picking" class="modal" @mousedown.self="closePicker">
        <div class="dialog" role="dialog" aria-label="Mover canción">
          <div class="cap">{{ copyMode ? 'Copiar canción' : 'Mover canción' }}</div>
          <div class="dlg-title">{{ picking.title }}</div>
          <div class="hint" style="margin-bottom: 8px">DESDE «{{ picking.from.toUpperCase() }}»</div>
          <input
            ref="pickInput"
            v-model="pickQuery"
            type="text"
            placeholder="Escribe para buscar la playlist destino…"
            spellcheck="false"
            @keydown="onPickKey"
          />
          <label class="check copyopt" title="La canción queda en la playlist de origen y también en la de destino">
            <input v-model="copyMode" type="checkbox" /> COPIAR EN VEZ DE MOVER (QUEDA EN LAS DOS PLAYLISTS)
          </label>
          <ul class="pick">
            <li
              v-for="(r, i) in pickResults"
              :key="r.id"
              :class="{ hot: i === pickIdx }"
              @mouseenter="pickIdx = i"
              @click="choosePick(i)"
            >
              <span class="pn">{{ r.name }}</span><span class="pc">{{ r.tracks.length }}</span>
            </li>
            <li
              v-if="canCreate"
              class="new"
              :class="{ hot: pickIdx === pickResults.length }"
              @mouseenter="pickIdx = pickResults.length"
              @click="choosePick(pickResults.length)"
            >
              + Crear «{{ pickQuery.trim() }}» y {{ copyMode ? 'copiar' : 'mover' }} aquí
            </li>
            <li v-if="!pickResults.length && !canCreate" class="none">Escribe un nombre.</li>
          </ul>
          <div class="row spread" style="margin-top: 10px">
            <span class="hint">↑↓ ELEGIR · ENTER {{ copyMode ? 'COPIAR' : 'MOVER' }} · ESC CANCELAR</span>
            <button class="btn small" @click="closePicker">Cancelar</button>
          </div>
        </div>
      </div>
  </Teleport>
</template>
