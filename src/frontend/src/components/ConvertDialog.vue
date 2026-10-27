<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../format'
import { current, forgetTrack, stop } from '../player/engine'
import { loadLibrary, toast } from '../store'
import type { ConvertFormat, ConvertMode, ConvertProgress, ConvertResult, LibPlaylist } from '../types'

// Convierte una playlist completa a otro formato (p. ej. WAV → MP3), conservando etiquetas, carátula, rejilla y cues.
const FORMATS: { id: ConvertFormat; label: string; lossy: boolean; rates: number[]; def: number }[] = [
  { id: 'mp3', label: 'MP3', lossy: true, rates: [320, 256, 192, 128], def: 320 },
  { id: 'm4a', label: 'M4A (AAC)', lossy: true, rates: [320, 256, 192, 128], def: 256 },
  { id: 'opus', label: 'OPUS', lossy: true, rates: [256, 192, 160, 128, 96], def: 160 },
  { id: 'flac', label: 'FLAC', lossy: false, rates: [], def: 0 },
  { id: 'wav', label: 'WAV', lossy: false, rates: [], def: 0 },
]
const LOSSY_EXT = ['mp3', 'm4a', 'aac', 'ogg', 'opus', 'wma']

const pl = ref<LibPlaylist | null>(null)
const format = ref<ConvertFormat>('mp3')
const bitrate = ref(320)
const mode = ref<ConvertMode>('keep')
const newName = ref('')
const nameTouched = ref(false)
const working = ref(false)
const progress = ref<ConvertProgress | null>(null)
const result = ref<ConvertResult | null>(null)
const error = ref('')
let off: (() => void) | null = null

const fmt = computed(() => FORMATS.find((f) => f.id === format.value)!)
const tracks = computed(() => pl.value?.tracks ?? [])
const todo = computed(() => tracks.value.filter((t) => t.ext.toLowerCase().replace('.', '') !== format.value))
const same = computed(() => tracks.value.length - todo.value.length)
const breakdown = computed(() => {
  const n = new Map<string, number>()
  for (const t of tracks.value) {
    const e = t.ext.toLowerCase().replace('.', '').toUpperCase()
    n.set(e, (n.get(e) ?? 0) + 1)
  }
  return [...n].map(([e, c]) => `${c} ${e}`).join(' · ')
})
const hasLossySource = computed(() => todo.value.some((t) => LOSSY_EXT.includes(t.ext.toLowerCase().replace('.', ''))))
const warning = computed(() => {
  if (!fmt.value.lossy && hasLossySource.value) return 'Pasar un MP3/M4A a FLAC o WAV no recupera calidad: el archivo solo pesa más.'
  if (fmt.value.lossy && hasLossySource.value) return 'Las canciones que ya eran con pérdida (MP3, M4A…) se vuelven a comprimir y pierden un poco más de calidad.'
  return ''
})
const defaultName = computed(() => `${pl.value?.name ?? ''} (${fmt.value.label.split(' ')[0]})`)
watch(defaultName, (v) => {
  if (!nameTouched.value) newName.value = v
})
watch(format, () => (bitrate.value = fmt.value.def))
const pct = computed(() => (progress.value && progress.value.total ? (progress.value.done / progress.value.total) * 100 : 0))
const sizeText = (n: number) => (n >= 1e9 ? (n / 1e9).toFixed(2) + ' GB' : (n / 1e6).toFixed(0) + ' MB')
const canGo = computed(() => !working.value && todo.value.length > 0 && (mode.value !== 'playlist' || newName.value.trim().length > 0))

function open(p: LibPlaylist) {
  pl.value = p
  const lossless = p.tracks.some((t) => !LOSSY_EXT.includes(t.ext.toLowerCase().replace('.', '')))
  format.value = lossless ? 'mp3' : 'flac'
  bitrate.value = fmt.value.def
  mode.value = 'keep'
  nameTouched.value = false
  newName.value = defaultName.value
  result.value = null
  error.value = ''
  progress.value = null
}
function close() {
  if (!working.value) pl.value = null
}

async function run() {
  const p = pl.value
  if (!p || !canGo.value) return
  working.value = true
  error.value = ''
  progress.value = { done: 0, total: todo.value.length, current: '' }
  off = api.onConvert((e) => (progress.value = e))
  const rels = todo.value.map((t) => t.path)
  try {
    // En Windows un archivo en uso no se puede mandar a la papelera: se suelta el que esté sonando.
    if (mode.value === 'replace') {
      const cur = current()
      if (cur && rels.includes(cur.track.path)) {
        stop()
        await new Promise((r) => setTimeout(r, 250))
      }
    }
    const res = await api.convertTracks({ rels, format: format.value, bitrate: bitrate.value, mode: mode.value, newName: newName.value.trim() })
    result.value = res
    if (mode.value === 'replace') rels.forEach(forgetTrack)
    await loadLibrary()
    toast(
      res.canceled
        ? `Conversión detenida: ${res.converted} canciones convertidas`
        : `${res.converted} canciones convertidas a ${fmt.value.label.split(' ')[0]}`,
      6000,
    )
  } catch (e) {
    error.value = errorText(e)
  } finally {
    off?.()
    off = null
    working.value = false
  }
}
const cancel = () => void api.cancelConvert()
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') close()
}
onBeforeUnmount(() => off?.())

defineExpose({ open })
</script>

<template>
  <Teleport to="body">
    <div v-if="pl" class="modal" @mousedown.self="close" @keydown="onKey">
      <div class="dialog convdlg" role="dialog" aria-label="Convertir playlist" tabindex="-1">
        <div class="cap">Convertir playlist</div>
        <div class="dlg-title">{{ pl.name }}</div>
        <div class="hint" style="margin-bottom: 8px">{{ tracks.length }} CANCIONES · {{ breakdown }}</div>

        <template v-if="!result">
          <div class="label">Convertir a</div>
          <div class="row" style="flex-wrap: wrap; gap: 6px">
            <button v-for="f in FORMATS" :key="f.id" class="tog m" :class="{ on: format === f.id }" :disabled="working" @click="format = f.id">{{ f.label }}</button>
          </div>
          <div v-if="fmt.lossy" class="row" style="margin-top: 8px; gap: 6px; flex-wrap: wrap">
            <span class="hint">CALIDAD</span>
            <button v-for="r in fmt.rates" :key="r" class="tog m" :class="{ on: bitrate === r }" :disabled="working" @click="bitrate = r">{{ r }} kbps</button>
          </div>
          <div v-else class="hint" style="margin-top: 8px">SIN PÉRDIDA: SE CONSERVA LA PROFUNDIDAD DE BITS DEL ORIGINAL</div>
          <div class="hint" style="margin-top: 6px">
            SE CONVIERTEN <b>{{ todo.length }}</b><template v-if="same"> · {{ same }} YA SON {{ fmt.label.split(' ')[0] }} Y SE DEJAN</template>
          </div>
          <div v-if="warning" class="warn" style="margin-top: 8px">{{ warning }}</div>

          <div class="label" style="margin-top: 12px">¿Qué hacer con los originales?</div>
          <label class="convopt" :class="{ on: mode === 'keep' }">
            <input v-model="mode" type="radio" value="keep" :disabled="working" />
            <span><b>Conservarlos.</b> Los convertidos quedan en la misma playlist, al lado de cada original.</span>
          </label>
          <label class="convopt" :class="{ on: mode === 'playlist' }">
            <input v-model="mode" type="radio" value="playlist" :disabled="working" />
            <span><b>Crear una playlist nueva</b> solo con los convertidos. La actual no se toca.</span>
          </label>
          <input
            v-if="mode === 'playlist'"
            v-model="newName"
            type="text"
            class="convname"
            placeholder="Nombre de la playlist nueva"
            spellcheck="false"
            :disabled="working"
            @input="nameTouched = true"
            @keydown.enter="run"
          />
          <label class="convopt" :class="{ on: mode === 'replace' }">
            <input v-model="mode" type="radio" value="replace" :disabled="working" />
            <span><b>Reemplazarlos.</b> Cada original se manda a la papelera del sistema (se puede recuperar desde ahí) cuando su versión convertida ya existe.</span>
          </label>

          <div v-if="working && progress" style="margin-top: 12px">
            <div class="loadbar"><i :style="{ width: pct + '%' }" /></div>
            <div class="hint" style="margin-top: 4px">{{ progress.done }}/{{ progress.total }} · {{ progress.current || '…' }}</div>
          </div>
          <div v-if="error" class="error" style="margin-top: 8px">{{ error }}</div>
          <div class="row spread" style="margin-top: 12px">
            <span class="hint">{{ working ? 'CONVIRTIENDO… NO CIERRES LA APP' : 'NUNCA SOBRESCRIBE: SI YA EXISTE, SE OMITE' }}</span>
            <span class="row">
              <button v-if="!working" class="btn small" @click="close">Cancelar</button>
              <button v-else class="btn small danger" @click="cancel">Detener</button>
              <button class="btn small primary" :disabled="!canGo" @click="run"><i v-if="working" class="spin" /> Convertir {{ todo.length }}</button>
            </span>
          </div>
        </template>

        <template v-else>
          <div class="nsum">
            <div>
              CONVERTIDAS: <b>{{ result.converted }}</b><template v-if="result.canceled"> (DETENIDO A MEDIAS)</template>
              <template v-if="result.converted"> · {{ sizeText(result.bytesBefore) }} → {{ sizeText(result.bytesAfter) }}</template>
            </div>
            <div v-if="result.folder">PLAYLIST NUEVA: <b>{{ result.folder }}</b></div>
            <div v-if="result.trashed">ORIGINALES EN LA PAPELERA: <b>{{ result.trashed }}</b></div>
            <div v-if="result.skipped?.length" class="hint" style="margin-top: 6px">OMITIDAS ({{ result.skipped.length }})</div>
            <ul v-if="result.skipped?.length" class="convlist">
              <li v-for="s in result.skipped.slice(0, 6)" :key="s">{{ s }}</li>
              <li v-if="result.skipped.length > 6">…y {{ result.skipped.length - 6 }} más</li>
            </ul>
            <div v-if="result.failed?.length" class="hint" style="margin-top: 6px; color: var(--err)">FALLARON ({{ result.failed.length }})</div>
            <ul v-if="result.failed?.length" class="convlist">
              <li v-for="s in result.failed.slice(0, 6)" :key="s">{{ s }}</li>
              <li v-if="result.failed.length > 6">…y {{ result.failed.length - 6 }} más</li>
            </ul>
          </div>
          <div class="row" style="margin-top: 12px; justify-content: flex-end"><button class="btn small primary" @click="pl = null">Cerrar</button></div>
        </template>
      </div>
    </div>
  </Teleport>
</template>
