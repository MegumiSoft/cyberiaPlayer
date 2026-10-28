<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../format'
import { current, forgetTrack, stop } from '../player/engine'
import { loadMetas } from '../player/trackmeta'
import { loadLibrary, toast } from '../store'
import type { ConvertFormat, ConvertFormatInfo, ConvertMode, ConvertProgress, ConvertResult, LibPlaylist } from '../types'

// Convierte una playlist completa a otro formato (p. ej. WAV → MP3), conservando etiquetas, carátula, rejilla y cues.
// La lista de formatos, sus calidades y qué extensiones son «con pérdida» las da el backend (ConvertFormats): no se duplican aquí.
const formats = ref<ConvertFormatInfo[]>([])
const lossyExts = ref<string[]>([])

const pl = ref<LibPlaylist | null>(null)
const format = ref<ConvertFormat>('mp3')
const bitrate = ref<number | null>(null) // kbps de tasa fija (null = el predeterminado del formato)
const vbr = ref<number | null>(null) // calidad VBR (solo MP3)
const mode = ref<ConvertMode>('keep')
const newName = ref('')
const nameTouched = ref(false)
const working = ref(false)
const confirming = ref(false)
const progress = ref<ConvertProgress | null>(null)
const result = ref<ConvertResult | null>(null)
const error = ref('')
const box = ref<HTMLElement>()
let off: (() => void) | null = null

const extOf = (t: { ext: string }) => t.ext.toLowerCase().replace('.', '')
const fmt = computed(() => formats.value.find((f) => f.id === format.value))
const fmtLabel = computed(() => (fmt.value?.label ?? format.value).split(' ')[0])
const tracks = computed(() => pl.value?.tracks ?? [])
const todo = computed(() => tracks.value.filter((t) => extOf(t) !== format.value)) // las que hay que convertir
const same = computed(() => tracks.value.length - todo.value.length) // las que ya están en el formato
// En «playlist nueva» entran TODAS (las que ya están en el formato se copian tal cual); en los otros modos solo las que cambian.
const rels = computed(() => (mode.value === 'playlist' ? tracks.value : todo.value).map((t) => t.path))
const breakdown = computed(() => {
  const n = new Map<string, number>()
  for (const t of tracks.value) {
    const e = extOf(t).toUpperCase()
    n.set(e, (n.get(e) ?? 0) + 1)
  }
  return [...n].map(([e, c]) => `${c} ${e}`).join(' · ')
})
const hasLossySource = computed(() => todo.value.some((t) => lossyExts.value.includes(extOf(t))))
const warning = computed(() => {
  if (!fmt.value) return ''
  if (!fmt.value.lossy && hasLossySource.value) return 'Pasar un MP3/M4A a FLAC o WAV no recupera calidad: el archivo solo pesa más.'
  if (fmt.value.lossy && hasLossySource.value) return 'Las canciones que ya eran con pérdida (MP3, M4A…) se vuelven a comprimir y pierden un poco más de calidad.'
  return ''
})
const defaultName = computed(() => `${pl.value?.name ?? ''} (${fmtLabel.value})`)
watch(defaultName, (v) => {
  if (!nameTouched.value) newName.value = v
})
watch(format, () => {
  bitrate.value = fmt.value?.lossy ? fmt.value.default : null
  vbr.value = null
})
watch([mode, format], () => (confirming.value = false))
const pickRate = (r: number) => ((bitrate.value = r), (vbr.value = null))
const pickVbr = (q: number) => ((vbr.value = q), (bitrate.value = null))
const pct = computed(() => (progress.value && progress.value.total ? (progress.value.done / progress.value.total) * 100 : 0))
const sizeText = (n: number) => (n >= 1e9 ? (n / 1e9).toFixed(2) + ' GB' : (n / 1e6).toFixed(0) + ' MB')
const canGo = computed(() => !working.value && !!fmt.value && rels.value.length > 0 && (mode.value !== 'playlist' || newName.value.trim().length > 0))

async function open(p: LibPlaylist) {
  if (!formats.value.length) {
    try {
      const info = await api.convertFormats()
      formats.value = info.formats
      lossyExts.value = info.lossyExts
    } catch (e) {
      toast(errorText(e))
      return
    }
  }
  pl.value = p
  // Si todo es con pérdida, FLAC/WAV solo harían crecer los archivos sin mejorar nada: M4A es el valor por defecto menos dañino.
  const hasLossless = p.tracks.some((t) => !lossyExts.value.includes(extOf(t)))
  format.value = hasLossless ? 'mp3' : 'm4a'
  bitrate.value = fmt.value?.default ?? null
  vbr.value = null
  mode.value = 'keep'
  confirming.value = false
  nameTouched.value = false
  newName.value = defaultName.value
  result.value = null
  error.value = ''
  progress.value = null
  await nextTick()
  box.value?.focus() // sin foco, Esc no llega al diálogo hasta que se hace clic dentro
}
function close() {
  if (!working.value) pl.value = null
}

// Con «Reemplazarlos» primero se pide confirmación (se mandan N originales a la papelera).
function start() {
  if (!canGo.value) return
  if (mode.value === 'replace' && !confirming.value) {
    confirming.value = true
    return
  }
  void run()
}

async function run() {
  const p = pl.value
  if (!p || !canGo.value) return
  confirming.value = false
  working.value = true
  error.value = ''
  progress.value = { done: 0, total: rels.value.length, current: '' }
  off = api.onConvert((e) => (progress.value = e))
  const list = rels.value
  try {
    // En Windows un archivo en uso no se puede mandar a la papelera, así que se suelta el que esté sonando. Los 250 ms
    // son una estimación de lo que tarda el sistema en liberarlo; si algo más lo retiene (una precarga o un análisis en
    // el backend), esa canción acaba en «fallaron» con el motivo: no es peligroso, solo puede requerir reintentar.
    if (mode.value === 'replace') {
      const cur = current()
      if (cur && list.includes(cur.track.path)) {
        stop()
        await new Promise((r) => setTimeout(r, 250))
      }
    }
    const res = await api.convertTracks({
      rels: list,
      format: format.value,
      bitrate: fmt.value?.lossy ? bitrate.value : null,
      vbr: fmt.value?.lossy ? vbr.value : null,
      mode: mode.value,
      newName: newName.value.trim(),
    })
    result.value = res
    if (mode.value === 'replace') list.forEach(forgetTrack)
    // El backend ya copió rejilla, cues y análisis a los archivos nuevos: hay que recargar también la caché de metadatos,
    // o la interfaz no los conoce y al reproducir una canción nueva guardaría un registro en blanco encima de lo copiado.
    await Promise.all([loadLibrary(), loadMetas()])
    toast(
      res.canceled ? `Conversión detenida: ${res.converted} canciones convertidas` : `${res.converted} canciones convertidas a ${fmtLabel.value}`,
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
    <div v-if="pl" class="modal" @mousedown.self="close">
      <div ref="box" class="dialog convdlg" role="dialog" aria-label="Convertir playlist" tabindex="-1" @keydown="onKey">
        <div class="cap">Convertir playlist</div>
        <div class="dlg-title">{{ pl.name }}</div>
        <div class="hint" style="margin-bottom: 8px">{{ tracks.length }} CANCIONES · {{ breakdown }}</div>

        <template v-if="!result">
          <div class="label">Convertir a</div>
          <div class="row" style="flex-wrap: wrap; gap: 6px">
            <button v-for="f in formats" :key="f.id" class="tog m" :class="{ on: format === f.id }" :disabled="working" @click="format = f.id">{{ f.label }}</button>
          </div>
          <div v-if="fmt?.lossy" class="row" style="margin-top: 8px; gap: 6px; flex-wrap: wrap">
            <span class="hint">CALIDAD</span>
            <button v-for="r in fmt.bitrates" :key="r" class="tog m" :class="{ on: bitrate === r }" :disabled="working" @click="pickRate(r)">{{ r }} kbps</button>
            <button v-for="q in fmt.vbr" :key="'v' + q" class="tog m" :class="{ on: vbr === q }" :disabled="working" title="Tasa variable: mejor calidad por tamaño" @click="pickVbr(q)">V{{ q }} · VBR</button>
          </div>
          <div v-else class="hint" style="margin-top: 8px">SIN PÉRDIDA: SE CONSERVA LA PROFUNDIDAD DE BITS DEL ORIGINAL</div>
          <div class="hint" style="margin-top: 6px">
            <template v-if="mode === 'playlist'">SE CONVIERTEN <b>{{ todo.length }}</b><template v-if="same"> · {{ same }} YA SON {{ fmtLabel }} Y SE COPIAN TAL CUAL</template></template>
            <template v-else>SE CONVIERTEN <b>{{ todo.length }}</b><template v-if="same"> · {{ same }} YA SON {{ fmtLabel }} Y SE DEJAN</template></template>
          </div>
          <div v-if="warning" class="warn" style="margin-top: 8px">{{ warning }}</div>
          <div v-if="fmt?.lossy" class="hint" style="margin-top: 6px">
            LOS CODIFICADORES CON PÉRDIDA AÑADEN ~25 MS DE RELLENO AL INICIO; LOS REPRODUCTORES CON «GAPLESS» LO DESCUENTAN. SI EL TUYO NO, REVISA LA REJILLA CON «AJUSTAR AL GOLPE».
          </div>

          <div class="label" style="margin-top: 12px">¿Qué hacer con los originales?</div>
          <label class="convopt" :class="{ on: mode === 'keep' }">
            <input v-model="mode" type="radio" value="keep" :disabled="working" />
            <span><b>Conservarlos.</b> Los convertidos quedan en la misma playlist, al lado de cada original.</span>
          </label>
          <label class="convopt" :class="{ on: mode === 'playlist' }">
            <input v-model="mode" type="radio" value="playlist" :disabled="working" />
            <span><b>Crear una playlist nueva</b> con todas las canciones (las que ya están en el formato se copian tal cual). La actual no se toca.</span>
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
            @keydown.enter="start"
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
          <div v-if="confirming" class="warn" style="margin-top: 10px">
            Se enviarán <b>{{ rels.length }}</b> originales a la papelera del sistema cuando su versión convertida esté lista. Si el original ya tenía pérdida, esa calidad no se recupera al vaciar la papelera. ¿Continuar?
          </div>
          <div class="row spread" style="margin-top: 12px">
            <span class="hint">{{ working ? 'CONVIRTIENDO… NO CIERRES LA APP' : 'NUNCA SOBRESCRIBE: SI YA EXISTE, SE OMITE' }}</span>
            <span class="row">
              <button v-if="confirming" class="btn small" @click="confirming = false">Volver</button>
              <button v-else-if="!working" class="btn small" @click="close">Cancelar</button>
              <button v-else class="btn small danger" @click="cancel">Detener</button>
              <button class="btn small primary" :class="{ danger: confirming }" :disabled="!canGo" @click="start">
                <i v-if="working" class="spin" /> {{ confirming ? 'Sí, convertir y reemplazar' : `Convertir ${rels.length}` }}
              </button>
            </span>
          </div>
        </template>

        <template v-else>
          <div class="nsum">
            <div>
              CONVERTIDAS: <b>{{ result.converted }}</b><template v-if="result.canceled"> (DETENIDO A MEDIAS)</template>
              <template v-if="result.converted"> · {{ sizeText(result.bytesBefore) }} → {{ sizeText(result.bytesAfter) }}</template>
            </div>
            <div v-if="result.copied">COPIADAS TAL CUAL (YA ERAN {{ fmtLabel }}): <b>{{ result.copied }}</b></div>
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
