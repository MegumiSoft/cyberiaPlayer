<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { api } from './api'
import ConfigPanel from './components/ConfigPanel.vue'
import ExportPanel from './components/ExportPanel.vue'
import FetchPanel from './components/FetchPanel.vue'
import MapPanel from './components/MapPanel.vue'
import PlayerView from './components/PlayerView.vue'
import QueuePanel from './components/QueuePanel.vue'
import { activeCount, init, loadLibrary, state, type Tab } from './store'

const tabs: { id: Tab; label: string }[] = [
  { id: 'fetch', label: 'FETCH' },
  { id: 'queue', label: 'QUEUE' },
  { id: 'player', label: 'PLAYER' },
  { id: 'map', label: 'MAP' },
  { id: 'export', label: 'EXPORT' },
  { id: 'config', label: 'CONFIG' },
]

const ready = computed(() => !!state.status)
const toolsOk = computed(() => !!state.status?.ytDlpPath && !!state.status?.ffmpegOk)

function open(t: Tab) {
  state.tab = t
  if (t === 'player' || t === 'map' || t === 'export') void loadLibrary()
}

function onWinKey(e: KeyboardEvent) {
  if (e.key === 'F11' || (e.key.toLowerCase() === 'f' && e.metaKey && e.ctrlKey)) {
    e.preventDefault()
    void api.toggleFullscreen()
  }
}
onMounted(() => {
  void init()
  window.addEventListener('keydown', onWinKey)
})
onBeforeUnmount(() => window.removeEventListener('keydown', onWinKey))
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <div class="brand">
        <b>CYBERIA</b>
        <span>LIBRARY MANAGEMENT SYSTEM</span>
      </div>
      <div class="led" :class="ready ? (toolsOk ? 'ok' : 'bad') : ''" :title="toolsOk ? 'yt-dlp y ffmpeg listos' : 'Falta yt-dlp o ffmpeg (ver CONFIG)'">
        <i /> {{ !ready ? '...' : toolsOk ? 'TOOLS OK' : 'TOOLS MISSING' }}
      </div>
      <button class="fsbtn" title="Pantalla completa / maximizar (F11 · Ctrl+⌘+F en Mac). Ctrl/⌘ + y − cambian el tamaño de las letras." aria-label="Pantalla completa" @click="api.toggleFullscreen()">
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M2 6V2h4M10 2h4v4M14 10v4h-4M6 14H2v-4" /></svg>
      </button>
      <nav class="tabs">
        <button v-for="t in tabs" :key="t.id" class="tab" :class="{ on: state.tab === t.id }" @click="open(t.id)">
          {{ t.label }}<span v-if="t.id === 'queue' && activeCount" class="n">{{ activeCount }}</span>
        </button>
      </nav>
    </header>

    <main class="content">
      <div v-show="state.tab === 'fetch'" class="view">
        <FetchPanel />
      </div>
      <div v-show="state.tab === 'queue'" class="view">
        <QueuePanel />
      </div>
      <div v-show="state.tab === 'player'" class="view" style="overflow-x: hidden; overflow-y: auto">
        <PlayerView :active="state.tab === 'player'" />
      </div>
      <div v-show="state.tab === 'map'" class="view" style="overflow: hidden">
        <MapPanel :active="state.tab === 'map'" />
      </div>
      <div v-show="state.tab === 'export'" class="view">
        <ExportPanel :active="state.tab === 'export'" />
      </div>
      <div v-show="state.tab === 'config'" class="view">
        <ConfigPanel />
      </div>
    </main>

    <div v-if="state.toast" class="toast">{{ state.toast }}</div>
  </div>
</template>
