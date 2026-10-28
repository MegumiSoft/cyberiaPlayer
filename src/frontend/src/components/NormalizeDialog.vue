<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { api } from '../api'
import { errorText } from '../format'
import { current, forgetTrack, player, playIndex, stop } from '../player/engine'
import { loadLibrary, state, toast } from '../store'
import type { LibTrack, Loudness, NormalizeResult } from '../types'

// Diálogo para igualar el volumen de una canción con el de otra de referencia. Sobrescribe el archivo.
const norm = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase()

const target = ref<LibTrack | null>(null)
const query = ref('')
const idx = ref(0)
const input = ref<HTMLInputElement>()
const ref_ = ref<{ track: LibTrack; playlist: string } | null>(null)
const tLoud = ref<Loudness | null>(null)
const rLoud = ref<Loudness | null>(null)
const measuring = ref(false)
const working = ref(false)
const error = ref('')
const result = ref<NormalizeResult | null>(null)

const all = computed(() => {
  const out: { track: LibTrack; playlist: string }[] = []
  for (const p of state.library.playlists) for (const t of p.tracks) if (t.path !== target.value?.path) out.push({ track: t, playlist: p.name })
  return out
})
const results = computed(() => {
  const q = norm(query.value.trim())
  const list = q ? all.value.filter((r) => norm(r.track.title).includes(q) || norm(r.playlist).includes(q)) : all.value
  return list.slice(0, 80)
})
watch(query, () => (idx.value = 0))

const gain = computed(() => (tLoud.value && rLoud.value ? rLoud.value.lufs - tLoud.value.lufs : null))
const fmt = (n: number, d = 1) => (n > 0 ? '+' : '') + n.toFixed(d)
const canGo = computed(() => !!ref_.value && gain.value !== null && Math.abs(gain.value) >= 0.3 && Math.abs(gain.value) <= 24 && !working.value && !measuring.value)

async function open(t: LibTrack) {
  target.value = t
  query.value = ''
  idx.value = 0
  ref_.value = null
  tLoud.value = rLoud.value = null
  error.value = ''
  result.value = null
  await nextTick()
  input.value?.focus()
  measuring.value = true
  try {
    tLoud.value = await api.measureLoudness(t.path)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    measuring.value = false
  }
}
const close = () => {
  if (!working.value) target.value = null
}

async function choose(i: number) {
  const r = results.value[i]
  if (!r) return
  ref_.value = r
  rLoud.value = null
  error.value = ''
  measuring.value = true
  try {
    rLoud.value = await api.measureLoudness(r.track.path)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    measuring.value = false
  }
}

function onKey(e: KeyboardEvent) {
  const n = results.value.length
  if (e.key === 'ArrowDown') (e.preventDefault(), (idx.value = Math.min(n - 1, idx.value + 1)))
  else if (e.key === 'ArrowUp') (e.preventDefault(), (idx.value = Math.max(0, idx.value - 1)))
  else if (e.key === 'Enter') (e.preventDefault(), void choose(idx.value))
  else if (e.key === 'Escape') close()
}

async function run() {
  const t = target.value
  const r = ref_.value
  if (!t || !r || !canGo.value) return
  working.value = true
  error.value = ''
  try {
    // Se suelta el archivo antes de reescribirlo (en Windows un archivo en uso no se puede reemplazar).
    const wasCurrent = current()?.track.path === t.path
    const idxNow = player.index
    if (wasCurrent) stop()
    await new Promise((res) => setTimeout(res, 200))
    result.value = await api.normalizeTrack(t.path, r.track.path)
    forgetTrack(t.path)
    await loadLibrary()
    toast(`«${t.title}» normalizada: ${fmt(result.value.gainDb)} dB`, 6000)
    if (wasCurrent && player.queue[idxNow]) playIndex(idxNow)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    working.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <Teleport to="body">
    <div v-if="target" class="modal" @mousedown.self="close">
      <div class="dialog normdlg" role="dialog" aria-label="Normalizar volumen">
        <div class="cap">Normalizar volumen</div>
        <div class="dlg-title">{{ target.title }}</div>
        <div class="hint" style="margin-bottom: 8px">
          ESTA CANCIÓN: <b v-if="tLoud">{{ tLoud.lufs.toFixed(1) }} LUFS</b><template v-else>{{ measuring ? 'MIDIENDO…' : '—' }}</template>
        </div>

        <template v-if="!result">
          <label class="label" for="nref" style="margin-top: 4px">Canción de referencia (con ese volumen quedará esta)</label>
          <input id="nref" ref="input" v-model="query" type="text" placeholder="Busca por título o playlist…" spellcheck="false" :disabled="working" @keydown="onKey" />
          <ul class="pick">
            <li v-for="(r, i) in results" :key="r.track.id" :class="{ hot: i === idx, sel: ref_?.track.path === r.track.path }" @mouseenter="idx = i" @click="choose(i)">
              <span class="pn">{{ r.track.title }}</span><span class="pc">{{ r.playlist }}</span>
            </li>
            <li v-if="!results.length" class="none">Sin resultados.</li>
          </ul>

          <div v-if="ref_" class="nsum">
            <div>REFERENCIA: <b>{{ ref_.track.title }}</b> · <b v-if="rLoud">{{ rLoud.lufs.toFixed(1) }} LUFS</b><template v-else>MIDIENDO…</template></div>
            <div v-if="gain !== null" class="g">
              AJUSTE: <b>{{ fmt(gain) }} dB</b>
              <span v-if="Math.abs(gain) < 0.3" class="hint"> · YA SUENAN IGUAL</span>
              <span v-else-if="Math.abs(gain) > 24" class="hint"> · DIFERENCIA DEMASIADO GRANDE</span>
              <span v-else-if="gain > 0" class="hint"> · SE USA UN LIMITADOR PARA NO DISTORSIONAR</span>
            </div>
          </div>

          <div class="warn" style="margin-top: 10px">
            Esto <b>sobrescribe el archivo</b> (se vuelve a codificar; en MP3/M4A hay una mínima pérdida extra de calidad). Antes se guarda una copia del original en la carpeta de datos de la app (las últimas 20). Se conservan etiquetas, carátula, rejilla y cues.
          </div>
          <div v-if="error" class="error" style="margin-top: 8px">{{ error }}</div>
          <div class="row spread" style="margin-top: 12px">
            <span class="hint">{{ working ? 'PROCESANDO… PUEDE TARDAR UN POCO' : '↑↓ ELEGIR · ENTER SELECCIONAR · ESC CERRAR' }}</span>
            <span class="row">
              <button class="btn small" :disabled="working" @click="close">Cancelar</button>
              <button class="btn small primary" :disabled="!canGo" @click="run"><i v-if="working" class="spin" /> Normalizar y sobrescribir</button>
            </span>
          </div>
        </template>

        <template v-else>
          <div class="nsum">
            <div>ANTES: <b>{{ result.before.toFixed(1) }} LUFS</b> → AHORA: <b>{{ result.after.toFixed(1) }} LUFS</b> (REFERENCIA {{ result.reference.toFixed(1) }})</div>
            <div>GANANCIA: <b>{{ fmt(result.gainDb) }} dB</b> · PICO {{ result.peak.toFixed(1) }} dBTP<span v-if="result.limited" class="hint"> · EL LIMITADOR CONTUVO PICOS</span></div>
            <div class="hint" style="margin-top: 6px; word-break: break-all">COPIA DEL ORIGINAL: {{ result.backup }}</div>
          </div>
          <div class="row" style="margin-top: 12px; justify-content: flex-end"><button class="btn small primary" @click="target = null">Cerrar</button></div>
        </template>
      </div>
    </div>
  </Teleport>
</template>
