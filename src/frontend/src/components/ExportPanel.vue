<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../format'
import { isAnalyzed } from '../player/libanalysis'
import { guarded, loadLibrary, state, toast } from '../store'
import type { LibTrack, USBInfo, USBJob, USBPreview, USBStatus, USBVolume } from '../types'

const props = defineProps<{ active: boolean }>()

const UNSUPPORTED = ['opus', 'ogg', 'oga']
const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()
const gb = (n: number) => (n >= 1e11 ? (n / 1e9).toFixed(0) : (n / 1e9).toFixed(1)) + ' GB'
const size = (n: number) => (n >= 1e9 ? (n / 1e9).toFixed(2) + ' GB' : (n / 1048576).toFixed(0) + ' MB')

// ---------------------------------------------------------------- destino
const volumes = ref<USBVolume[]>([])
const dest = ref('')
const info = ref<USBInfo | null>(null)
const deviceName = ref('CYBERIA')
// Color de la onda en el reproductor (se guarda en DEVSETTING.DAT de la memoria) y formato de la tonalidad (el de CONFIG).
type Wave = 'blue' | 'rgb' | '3band'
const WAVES: { id: Wave; label: string; hint: string }[] = [
  { id: 'blue', label: 'AZUL', hint: 'Onda azul clásica' },
  { id: 'rgb', label: 'RGB', hint: 'Onda de color (CDJ-2000NXS2 y posteriores)' },
  { id: '3band', label: '3 BANDAS', hint: 'Graves / medios / agudos por separado (CDJ-3000)' },
]
let savedWave: Wave = 'rgb'
try {
  const v = localStorage.getItem('cyberia.wave')
  if (v === 'blue' || v === 'rgb' || v === '3band') savedWave = v
} catch {
  /* sin almacenamiento */
}
const waveform = ref<Wave>(savedWave)
watch(waveform, (v) => {
  try {
    localStorage.setItem('cyberia.wave', v)
  } catch {
    /* sin almacenamiento */
  }
})
const keyNote = computed(() => (state.settings?.keyNotation === 'note' ? 'NOTAS (Am, F#m)' : 'CAMELOT (8A, 11B)'))
const vol = computed(() => volumes.value.find((v) => v.path === dest.value) ?? null)
const freeBytes = computed(() => info.value?.free || vol.value?.free || 0)

async function refreshVolumes() {
  volumes.value = (await guarded(() => api.usbVolumes())) ?? []
  // Si solo hay una memoria y no se ha elegido ninguna, se elige sola.
  if (!dest.value && volumes.value.length === 1) dest.value = volumes.value[0].path
  if (dest.value && !volumes.value.some((v) => v.path === dest.value) && !customDest.value) dest.value = ''
}
const customDest = ref(false)
async function inspect() {
  info.value = dest.value ? await api.usbInspect(dest.value).catch(() => null) : null
}
async function pickFolder() {
  const d = await guarded(() => api.chooseFolder())
  if (d) {
    customDest.value = true
    dest.value = d
  }
}
function pickVolume(v: USBVolume) {
  customDest.value = false
  dest.value = v.path
}
const existing = computed(() => (info.value?.playlists ?? []).filter((p) => !p.folder))

// ---------------------------------------------------------------- contenido
const view = ref<'playlists' | 'tracks'>('playlists')
const filter = ref('')
const picked = reactive<Record<string, boolean>>({}) // playlists completas (por ruta de carpeta)
const pickedTr = reactive<Record<string, boolean>>({}) // canciones sueltas (por ruta)
const srcPl = ref('') // en la vista de canciones: ruta de la playlist de origen ('' = todas)
const target = ref<{ kind: 'new' } | { kind: 'existing'; id: number }>({ kind: 'new' })
const newName = ref('Cyberia')

const byPath = computed(() => {
  const m = new Map<string, LibTrack>()
  for (const p of state.library.playlists) for (const t of p.tracks) m.set(t.path, t)
  return m
})

const rows = computed(() =>
  state.library.playlists.map((p) => {
    const ok = p.tracks.filter((t) => !UNSUPPORTED.includes(t.ext))
    return {
      path: p.path, name: p.name, total: p.tracks.length, supported: ok.length,
      pending: ok.filter((t) => !isAnalyzed(t)).length, bytes: ok.reduce((n, t) => n + t.size, 0),
    }
  }),
)
const shownRows = computed(() => {
  const q = norm(filter.value.trim())
  return q ? rows.value.filter((r) => norm(r.name).includes(q)) : rows.value
})
const MAXROWS = 500
const trackRows = computed(() => {
  const q = norm(filter.value.trim())
  const out: { t: LibTrack; pl: string }[] = []
  for (const p of state.library.playlists) {
    if (srcPl.value && p.path !== srcPl.value) continue
    for (const t of p.tracks) if (!q || norm(t.title).includes(q) || norm(p.name).includes(q)) out.push({ t, pl: p.name })
  }
  return out
})
const shownTracks = computed(() => trackRows.value.slice(0, MAXROWS))
const nPicked = computed(() => rows.value.filter((r) => picked[r.path]).length)
const nTracks = computed(() => Object.values(pickedTr).filter(Boolean).length)

function setAll(on: boolean) {
  if (view.value === 'playlists') for (const r of shownRows.value) picked[r.path] = on
  else for (const r of trackRows.value) pickedTr[r.t.path] = on
}

const targetName = computed(() => {
  if (target.value.kind === 'new') return newName.value.trim() || 'Cyberia'
  const id = target.value.id
  return existing.value.find((p) => p.id === id)?.name ?? ''
})

const jobs = computed<USBJob[]>(() => {
  const out: USBJob[] = []
  for (const p of state.library.playlists) {
    if (picked[p.path]) out.push({ name: p.name, existingId: 0, tracks: p.tracks.map((t) => t.path) })
  }
  const tr = Object.keys(pickedTr).filter((k) => pickedTr[k] && byPath.value.has(k))
  if (tr.length) out.push({ name: target.value.kind === 'new' ? targetName.value : '', existingId: target.value.kind === 'existing' ? target.value.id : 0, tracks: tr })
  return out
})

// ---------------------------------------------------------------- resumen
const preview = ref<USBPreview | null>(null)
const previewing = ref(false)
let pvTimer: ReturnType<typeof setTimeout> | undefined
let pvSeq = 0
function schedulePreview() {
  clearTimeout(pvTimer)
  if (!jobs.value.length) {
    preview.value = null
    previewing.value = false
    return
  }
  previewing.value = true
  pvTimer = setTimeout(async () => {
    const seq = ++pvSeq
    try {
      const r = await api.usbPreview({ dest: dest.value, deviceName: deviceName.value, jobs: jobs.value, waveform: waveform.value })
      if (seq === pvSeq) preview.value = r
    } catch {
      if (seq === pvSeq) preview.value = null
    } finally {
      if (seq === pvSeq) previewing.value = false
    }
  }, 250)
}
watch([jobs, dest, info], schedulePreview, { deep: true })

const lists = computed(() => preview.value?.lists ?? [])
const toAdd = computed(() => lists.value.reduce((n, l) => n + (l.tracks?.length ?? 0), 0))
const unanalyzed = computed(() => {
  const seen = new Set<string>()
  let n = 0
  for (const j of jobs.value)
    for (const p of j.tracks) {
      if (seen.has(p)) continue
      seen.add(p)
      const t = byPath.value.get(p)
      if (t && !UNSUPPORTED.includes(t.ext) && !isAnalyzed(t)) n++
    }
  return n
})
const tooBig = computed(() => !!preview.value && freeBytes.value > 0 && preview.value.copyBytes > freeBytes.value)

// ---------------------------------------------------------------- exportar
const st = ref<USBStatus | null>(null)
let off: (() => void) | undefined
const running = computed(() => !!st.value?.running)
const pct = computed(() => (st.value && st.value.total ? Math.round((st.value.done / st.value.total) * 100) : 0))
const canGo = computed(() => !!dest.value && !!info.value?.exists && jobs.value.length > 0 && !!preview.value && toAdd.value > 0 && !running.value && !tooBig.value && !previewing.value)

async function go() {
  try {
    st.value = { running: true, done: 0, total: 0, message: 'Preparando…', error: '' }
    await api.startUsbExport({ dest: dest.value, deviceName: deviceName.value.trim() || 'CYBERIA', jobs: jobs.value, waveform: waveform.value })
  } catch (e) {
    st.value = { running: false, done: 0, total: 0, message: '', error: errorText(e) }
    toast(errorText(e))
  }
}

watch(dest, () => void inspect())
watch(
  () => props.active,
  (a) => {
    if (a) {
      void loadLibrary()
      void refreshVolumes()
      void inspect()
    }
  },
)
let poll: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  off = api.onUsb((s) => {
    st.value = s
    if (!s.running) {
      void inspect()
      void refreshVolumes()
      schedulePreview()
    }
  })
  const cur = await api.usbState().catch(() => null)
  if (cur && (cur.running || cur.report || cur.error)) st.value = cur
  void refreshVolumes()
  // Detecta memorias que se conectan o se sacan mientras la pantalla está abierta.
  poll = setInterval(() => {
    if (props.active && !running.value) void refreshVolumes()
  }, 4000)
})
onUnmounted(() => {
  off?.()
  clearInterval(poll)
})
</script>

<template>
  <div class="exportpage">
    <div class="exmain">
      <div class="panel-box">
        <div class="cap">Destino · memoria USB</div>
        <div class="tiles">
          <button v-for="v in volumes" :key="v.path" class="tile" :class="{ on: dest === v.path }" :title="v.path" @click="pickVolume(v)">
            <svg class="usb" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><rect x="7" y="9" width="10" height="13" rx="1.5" /><path d="M9.5 9V3h5v6M10.5 5.5v1M13.5 5.5v1" /></svg>
            <b>{{ v.name }}</b>
            <span class="free">{{ v.total ? `${gb(v.free)} libres · ${gb(v.total)}` : '' }}</span>
            <span v-if="v.total" class="spacebar"><i :style="{ width: Math.min(100, ((v.total - v.free) / v.total) * 100) + '%' }" /></span>
            <span v-if="v.hasPioneer" class="tag">BIBLIOTECA PIONEER</span>
          </button>
          <button class="tile alt" :class="{ on: customDest && !!dest }" title="Elegir otra carpeta como destino" @click="pickFolder">
            <svg class="usb" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6.5A1.5 1.5 0 0 1 4.5 5H10l2 2.5h7.5A1.5 1.5 0 0 1 21 9v9a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18z" /></svg>
            <b>Otra carpeta…</b>
            <span class="free">{{ customDest && dest ? dest : '' }}</span>
          </button>
          <button class="tile alt refresh" title="Volver a buscar unidades" @click="refreshVolumes"><b>↻</b><span class="free">BUSCAR</span></button>
        </div>
        <div v-if="!volumes.length" class="hint" style="margin-top: 8px">NO SE DETECTA NINGUNA MEMORIA: CONÉCTALA Y SE MOSTRARÁ SOLA (O USA «OTRA CARPETA»).</div>
        <div v-if="dest && info && !info.exists" class="warn">No se puede acceder a esa unidad.</div>
        <div v-if="info?.readError" class="warn">La memoria tiene una biblioteca que no se pudo leer ({{ info.readError }}). No se modificará nada hasta resolverlo.</div>
        <div v-if="info?.hasLibrary" class="note">
          Esta memoria ya tiene una biblioteca Pioneer ({{ existing.length }} playlists). Se <b>conserva todo</b> y solo se agrega lo nuevo; al terminar queda una copia de la base anterior (export.pdb.bak).
        </div>
        <div v-if="info?.hasOneLibrary" class="warn">También tiene una biblioteca OneLibrary (exportLibrary.db): esta app no la actualiza. Los equipos con biblioteca clásica (CDJ-2000NXS2, CDJ-3000, XDJ-1000MK2…) sí verán lo nuevo.</div>
        <div class="wavepick">
          <span class="label" style="margin: 0">Onda en el reproductor</span>
          <div class="seg">
            <button v-for="w in WAVES" :key="w.id" :class="[w.id, { on: waveform === w.id }]" :title="w.hint" @click="waveform = w.id">{{ w.label }}</button>
          </div>
          <span class="hint">TONALIDAD: {{ keyNote }} · SE CAMBIA EN CONFIG</span>
        </div>
        <div v-if="dest && !info?.hasLibrary" class="row" style="margin-top: 10px">
          <label class="label" for="devname" style="margin: 0">Nombre del dispositivo</label>
          <input id="devname" v-model="deviceName" type="text" maxlength="40" spellcheck="false" style="max-width: 220px" />
        </div>
      </div>

      <div class="panel-box">
        <div class="viewtabs">
          <button :class="{ on: view === 'playlists' }" @click="view = 'playlists'">PLAYLISTS<span v-if="nPicked" class="n">{{ nPicked }}</span></button>
          <button :class="{ on: view === 'tracks' }" @click="view = 'tracks'">CANCIONES<span v-if="nTracks" class="n">{{ nTracks }}</span></button>
          <span class="hint vt-hint">AMBAS VISTAS SE SUMAN EN EL RESUMEN</span>
        </div>

        <div class="row" style="margin: 10px 0 8px; flex-wrap: nowrap">
          <input v-model="filter" class="grow search" type="text" :placeholder="view === 'playlists' ? 'Filtrar playlists…' : 'Filtrar canciones o playlists…'" spellcheck="false" />
          <select v-if="view === 'tracks'" v-model="srcPl" class="srcsel" title="Mostrar solo las canciones de una playlist">
            <option value="">Todas las playlists</option>
            <option v-for="p in state.library.playlists" :key="p.path" :value="p.path">{{ p.name }}</option>
          </select>
          <button class="btn small" @click="setAll(true)">{{ filter || srcPl ? 'Marcar visibles' : 'Todas' }}</button>
          <button class="btn small" @click="setAll(false)">Ninguna</button>
        </div>

        <div v-if="!rows.length" class="empty">No hay música en la biblioteca.</div>

        <ul v-if="view === 'playlists'" class="explist">
          <li v-for="r in shownRows" :key="r.path" :class="{ sel: picked[r.path] }">
            <label>
              <input v-model="picked[r.path]" type="checkbox" />
              <span class="nm">{{ r.name }}</span>
              <span class="meta">
                {{ r.total }} canc. · {{ size(r.bytes) }}
                <template v-if="r.pending"> · <b class="amber">{{ r.pending }} sin analizar</b></template>
                <template v-if="r.total > r.supported"> · <b class="amber">{{ r.total - r.supported }} no compatibles</b></template>
              </span>
            </label>
          </li>
          <li v-if="rows.length && !shownRows.length" class="none">Ninguna playlist coincide.</li>
        </ul>

        <div v-else class="trkview">
          <div class="targets">
            <div class="cap2">Mandar las canciones marcadas a…</div>
            <button class="tgt" :class="{ on: target.kind === 'new' }" @click="target = { kind: 'new' }">
              <span class="plus">＋</span><b>Playlist nueva</b>
            </button>
            <input v-if="target.kind === 'new'" v-model="newName" type="text" maxlength="60" spellcheck="false" placeholder="Nombre" class="tgtname" />
            <div class="cap2" style="margin-top: 10px">En la memoria{{ info?.hasLibrary ? '' : ' (no hay playlists)' }}</div>
            <div class="tgtlist">
              <button v-for="p in existing" :key="p.id" class="tgt" :class="{ on: target.kind === 'existing' && target.id === p.id }" :title="p.name" @click="target = { kind: 'existing', id: p.id }">
                <b>{{ p.parent ? p.parent + ' / ' : '' }}{{ p.name }}</b><span class="c">{{ p.count }}</span>
              </button>
            </div>
          </div>
          <ul class="explist tl">
            <li v-for="r in shownTracks" :key="r.t.path" :class="{ sel: pickedTr[r.t.path] }">
              <label>
                <input v-model="pickedTr[r.t.path]" type="checkbox" />
                <span class="nm" :class="{ todo: !isAnalyzed(r.t) }">{{ r.t.title }}</span>
                <span class="meta">{{ r.pl }}<template v-if="UNSUPPORTED.includes(r.t.ext)"> · <b class="amber">no compatible</b></template></span>
              </label>
            </li>
            <li v-if="trackRows.length > MAXROWS" class="none">Se muestran {{ MAXROWS }} de {{ trackRows.length }}: usa el filtro para encontrar más.</li>
            <li v-if="!trackRows.length" class="none">Ninguna canción coincide.</li>
          </ul>
        </div>
      </div>
    </div>

    <aside class="exside">
      <div class="panel-box sumbox">
        <div class="cap">Resumen de exportación</div>
        <div class="sumdest">
          <template v-if="dest">
            <b>{{ vol?.name || dest }}</b>
            <span v-if="freeBytes" class="hint">{{ gb(freeBytes) }} LIBRES</span>
          </template>
          <span v-else class="hint">ELIGE UNA MEMORIA</span>
        </div>

        <div v-if="!jobs.length" class="empty" style="padding: 14px 0">Marca playlists o canciones para ver aquí qué se exportará.</div>
        <div v-else class="sumlists">
          <div v-for="l in lists" :key="l.name" class="sumlist">
            <div class="sl-head">
              <b>{{ l.name || '(sin nombre)' }}</b>
              <span class="badge" :class="l.existing ? 'ex' : 'nw'">{{ l.existing ? 'EXISTENTE' : 'NUEVA' }}</span>
              <span class="hint">+{{ l.tracks?.length ?? 0 }}</span>
            </div>
            <ul v-if="l.tracks?.length" class="sl-tracks">
              <li v-for="(t, i) in l.tracks" :key="i" :title="t.title">
                <span class="tt">{{ t.title }}</span>
                <span v-if="!t.copy" class="ons">YA EN USB</span>
              </li>
            </ul>
            <div v-if="l.inList" class="hint">{{ l.inList }} YA ESTABAN EN ESTA PLAYLIST</div>
            <div v-if="!l.tracks?.length" class="hint">NADA NUEVO QUE AÑADIR</div>
          </div>
        </div>

        <div v-if="jobs.length" class="sumtot">
          <div><b>{{ preview?.copyTracks ?? '…' }}</b> canciones a copiar · <b>{{ preview ? size(preview.copyBytes) : '…' }}</b></div>
          <div v-if="preview && toAdd > preview.copyTracks" class="hint">{{ toAdd - preview.copyTracks }} ENTRADAS USAN ARCHIVOS QUE YA ESTÁN EN LA MEMORIA</div>
          <div v-if="tooBig" class="warn">No cabe: faltan {{ size((preview?.copyBytes ?? 0) - freeBytes) }} de espacio.</div>
          <div v-if="unanalyzed" class="hint amberh">{{ unanalyzed }} SIN ANALIZAR: SE COPIAN SIN ONDA NI REJILLA (USA «ANALIZAR TODAS» EN PLAYER)</div>
          <div v-if="preview?.unsupported" class="hint amberh">{{ preview.unsupported }} NO COMPATIBLES CON CDJ (SE OMITEN)</div>
        </div>

        <div class="row" style="margin-top: 12px">
          <button class="btn primary big" :disabled="!canGo" @click="go">{{ running ? 'Exportando…' : 'Exportar a la USB' }}</button>
          <button v-if="running" class="btn" @click="api.cancelUsbExport()">Cancelar</button>
        </div>
        <div v-if="!canGo && !running && jobs.length && !dest" class="hint" style="margin-top: 6px">ELIGE PRIMERO LA MEMORIA USB.</div>
        <div v-if="running" class="prog">
          <div class="bar"><div class="bar-fill" :style="{ width: pct + '%' }" /></div>
          <div class="hint">{{ st?.done }} / {{ st?.total }} · {{ st?.message }}</div>
        </div>
        <div v-if="st && !st.running && st.error" class="error">{{ st.error }}</div>
        <div v-if="st && !st.running && st.report && !st.error" class="done">
          <b>Listo.</b>
          {{ st.report.tracks }} canciones nuevas<template v-if="st.report.reused"> ({{ st.report.reused }} ya estaban)</template> ·
          {{ st.report.playlists }} playlists nuevas<template v-if="st.report.extended"> · {{ st.report.extended }} ampliadas</template> ·
          {{ st.report.analyzed }} con análisis · {{ size(st.report.bytes) }} copiados.
          <div class="hint" style="margin-top: 6px">EXPULSA LA MEMORIA CON LA OPCIÓN DEL SISTEMA ANTES DE SACARLA.</div>
          <ul v-if="st.report.warnings?.length" class="warnlist">
            <li v-for="w in st.report.warnings" :key="w">{{ w }}</li>
          </ul>
          <details v-if="st.report.skipped?.length" class="diag" open>
            <summary>No exportadas ({{ st.report.skipped.length }})</summary>
            <pre>{{ st.report.skipped.join('\n') }}</pre>
          </details>
        </div>
      </div>
    </aside>
  </div>
</template>
