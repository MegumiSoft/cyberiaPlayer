<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '../api'
import { errorText, formatDuration } from '../format'
import { guarded, state, toast } from '../store'
import type { NewJob, PreviewResult } from '../types'
import banner from '../assets/cyberia-banner.webp'

const input = ref('')
const loading = ref(false)
const results = ref<PreviewResult[]>([])
// Canciones marcadas, con clave "<resultado>:<posición>". Por defecto todas.
const selected = reactive<Record<string, boolean>>({})
const broken = reactive<Record<string, boolean>>({})

const urls = computed(() =>
  input.value
    .split(/\s+/)
    .map((u) => u.trim())
    .filter(Boolean),
)

const key = (r: number, e: number) => `${r}:${e}`
const selectedCount = computed(() => Object.values(selected).filter(Boolean).length)

async function analyze() {
  if (!urls.value.length || loading.value) return
  loading.value = true
  results.value = []
  for (const k of Object.keys(selected)) delete selected[k]
  try {
    const found = (await api.previewUrls([...new Set(urls.value)])) ?? []
    results.value = found
    found.forEach((res, r) => res.preview?.entries.forEach((_, e) => (selected[key(r, e)] = true)))
  } catch (e) {
    toast(errorText(e))
  } finally {
    loading.value = false
  }
}

function setAll(r: number, value: boolean) {
  results.value[r].preview?.entries.forEach((_, e) => (selected[key(r, e)] = value))
}

function selectedIn(r: number): number {
  return results.value[r].preview?.entries.filter((_, e) => selected[key(r, e)]).length ?? 0
}

async function addToQueue() {
  const items: NewJob[] = []
  results.value.forEach((res, r) => {
    const p = res.preview
    if (!p) return
    const spotify = p.source === 'spotify'
    p.entries.forEach((entry, e) => {
      if (!selected[key(r, e)]) return
      const artist = entry.uploader || p.uploader
      const job: NewJob = {
        url: entry.url,
        title: entry.title,
        uploader: artist,
        thumbnail: entry.thumbnail,
        folder: p.isPlaylist ? p.title : '',
        index: p.isPlaylist ? entry.index : 0,
      }
      if (spotify) {
        // Spotify solo da los metadatos: el audio se busca en YouTube y se guarda con el nombre "Artista - Título".
        job.search = true
        job.duration = entry.duration
        job.outName = artist ? `${artist} - ${entry.title}` : entry.title
      }
      items.push(job)
    })
  })
  if (!items.length) return
  const added = await guarded(() => api.enqueue(items))
  if (added === undefined) return
  const skipped = items.length - added
  toast(
    skipped > 0
      ? `${added} añadidas a la cola (${skipped} ya estaban)`
      : `${added} ${added === 1 ? 'canción añadida' : 'canciones añadidas'} a la cola`,
  )
  results.value = []
  input.value = ''
  state.tab = 'queue'
}
</script>

<template>
  <section class="panel-box">
    <div class="cap">Source</div>
    <label class="label" for="urls" style="margin-top: 0">URLs — una por línea (SoundCloud, YouTube, Spotify)</label>
    <div class="urlrow">
      <textarea
        id="urls"
        v-model="input"
        rows="12"
        spellcheck="false"
        placeholder="https://soundcloud.com/usuario/sets/mi-playlist&#10;https://www.youtube.com/watch?v=…&#10;https://open.spotify.com/playlist/…"
        @keydown.ctrl.enter="analyze"
        @keydown.meta.enter="analyze"
      />
      <img class="mascot" :src="banner" alt="Cyberia Library Management System" draggable="false" />
    </div>
    <div class="row" style="margin-top: 8px">
      <button class="btn primary" :disabled="!urls.length || loading" @click="analyze">
        {{ loading ? 'Analizando…' : `Analizar${urls.length > 1 ? ' ' + urls.length + ' URLs' : ''}` }}
      </button>
      <span class="hint">CTRL/⌘ + ENTER</span>
    </div>

    <div v-for="(res, r) in results" :key="res.url" class="cardbox">
      <template v-if="res.preview">
        <div class="row spread" style="align-items: flex-start">
          <div style="min-width: 0">
            <div class="title">
              {{ res.preview.title }}
              <span class="tag">{{ res.preview.source === 'spotify' ? 'SPOTIFY' : res.preview.isPlaylist ? 'PLAYLIST' : 'TRACK' }}</span>
            </div>
            <div class="sub">
              {{ res.preview.uploader }}
              <template v-if="res.preview.isPlaylist"> · {{ res.preview.entries.length }} canciones</template>
            </div>
          </div>
          <div v-if="res.preview.isPlaylist" class="row">
            <button class="btn small" @click="setAll(r, true)">Todas</button>
            <button class="btn small" @click="setAll(r, false)">Ninguna</button>
          </div>
        </div>
        <div v-if="res.preview.warning" class="warn">
          yt-dlp no pudo leer el video: «{{ res.preview.warning }}». Se muestra solo la información pública; la descarga probablemente
          fallará hasta que elijas un navegador para las cookies en CONFIG → YouTube (o actualices yt-dlp).
        </div>
        <ul class="entries">
          <li v-for="(entry, e) in res.preview.entries" :key="entry.url + e">
            <label class="entry">
              <input v-model="selected[key(r, e)]" type="checkbox" />
              <span v-if="res.preview.isPlaylist" class="num">{{ String(entry.index).padStart(2, '0') }}</span>
              <img
                v-if="entry.thumbnail && !broken[entry.thumbnail]"
                class="thumb"
                :src="entry.thumbnail"
                alt=""
                loading="lazy"
                referrerpolicy="no-referrer"
                @error="broken[entry.thumbnail] = true"
              />
              <span v-else class="thumb none">♪</span>
              <span class="entry-title">
                {{ entry.title }}
                <span v-if="res.preview.source === 'spotify' && entry.uploader" class="sub"> — {{ entry.uploader }}</span>
              </span>
              <span class="dur">{{ formatDuration(entry.duration) }}</span>
            </label>
          </li>
        </ul>
        <div v-if="res.preview.isPlaylist" class="hint" style="margin-top: 6px">
          {{ selectedIn(r) }} / {{ res.preview.entries.length }} SELECCIONADAS
        </div>
        <div v-if="res.preview.source === 'spotify'" class="hint" style="margin-top: 4px">
          SPOTIFY NO PERMITE BAJAR AUDIO: SE BUSCA CADA TEMA EN YOUTUBE Y SE DESCARTA SI NO COINCIDE CLARAMENTE.
        </div>
      </template>
      <template v-else>
        <div class="title" style="word-break: break-all">{{ res.url }}</div>
        <div class="error">{{ res.error }}</div>
      </template>
    </div>

    <div v-if="results.some((x) => x.preview)" class="sticky-bar">
      <button class="btn primary big" :disabled="!selectedCount" @click="addToQueue">
        Añadir {{ selectedCount }} {{ selectedCount === 1 ? 'canción' : 'canciones' }} a la cola
      </button>
    </div>
  </section>
</template>
