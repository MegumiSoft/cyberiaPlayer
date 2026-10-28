<script setup lang="ts">
import { computed, ref } from 'vue'
import { api } from '../api'
import { player } from '../player/engine'
import { guarded, loadLibrary, state, toast } from '../store'
import type { Settings } from '../types'
import { FORMATS, LOSSLESS, QUALITIES } from '../types'

const s = computed(() => state.settings)
const st = computed(() => state.status)
const lossless = computed(() => !!s.value && LOSSLESS.includes(s.value.format))
const spBusy = ref(false)
const BROWSERS = [
  { id: 'chrome', label: 'Chrome' },
  { id: 'firefox', label: 'Firefox' },
  { id: 'brave', label: 'Brave' },
  { id: 'edge', label: 'Edge' },
  { id: 'chromium', label: 'Chromium' },
  { id: 'opera', label: 'Opera' },
  { id: 'vivaldi', label: 'Vivaldi' },
]
const MASCOTS = [
  { id: 'miku', label: 'HATSUNE MIKU' },
  { id: 'gir', label: 'GIR' },
  { id: 'cat', label: 'BREAKDANCE CAT' },
  { id: 'none', label: 'NINGUNA' },
]

const ffmpegHint = computed(() => {
  switch (st.value?.platform) {
    case 'windows':
      return 'winget install Gyan.FFmpeg'
    case 'darwin':
      return 'brew install ffmpeg'
    default:
      return 'sudo apt install ffmpeg'
  }
})

async function save(patch: Partial<Settings>) {
  if (!state.settings) return
  const saved = await guarded(() => api.saveSettings({ ...state.settings!, ...patch }))
  if (saved) state.settings = saved
  state.status = (await guarded(() => api.status())) ?? state.status
}

async function chooseFolder() {
  const dir = await guarded(() => api.chooseFolder())
  if (dir) {
    await save({ outDir: dir })
    await loadLibrary()
  }
}

async function chooseFFmpeg() {
  const file = await guarded(() => api.chooseFFmpeg())
  if (file) await save({ ffmpegPath: file })
}

async function updateTools() {
  const msg = await guarded(() => api.updateTools())
  if (msg) toast(msg.split('\n').pop() || msg)
  state.status = (await guarded(() => api.status())) ?? state.status
}

function onConcurrency(e: Event) {
  save({ concurrency: Number((e.target as HTMLInputElement).value) })
}

const redirect = computed(() => st.value?.redirectUri || 'http://127.0.0.1:8888/callback')
async function copyRedirect() {
  const ok = await guarded(async () => (await api.copyText(redirect.value), true))
  if (ok) toast('Redirect URI copiada al portapapeles')
}

async function spotifyTest() {
  spBusy.value = true
  const msg = await guarded(() => api.spotifyTest())
  spBusy.value = false
  if (msg) toast(msg)
}

async function spotifyLogin() {
  spBusy.value = true
  toast('Se abrió el navegador: autoriza la app de Spotify y vuelve aquí')
  const user = await guarded(() => api.spotifyLogin())
  spBusy.value = false
  state.settings = (await guarded(() => api.getSettings())) ?? state.settings
  if (user) toast(`Sesión iniciada: ${user}`)
}

async function spotifyLogout() {
  await guarded(() => api.spotifyLogout())
  state.settings = (await guarded(() => api.getSettings())) ?? state.settings
}
</script>

<template>
  <section v-if="s" class="grid2">
    <div>
      <div class="panel-box">
        <div class="cap">Library &amp; downloads</div>

        <label class="label" style="margin-top: 0">Carpeta de la biblioteca (cada subcarpeta es una playlist)</label>
        <div class="row">
          <input class="grow" type="text" :value="s.outDir" readonly />
          <button class="btn" @click="chooseFolder">Cambiar…</button>
          <button class="btn" @click="guarded(() => api.openDownloads())">Abrir</button>
        </div>

        <div class="grid3">
          <div>
            <label class="label" for="fmt">Formato</label>
            <select id="fmt" :value="s.format" @change="save({ format: ($event.target as HTMLSelectElement).value })">
              <option v-for="f in FORMATS" :key="f" :value="f">{{ f.toUpperCase() }}</option>
            </select>
          </div>
          <div>
            <label class="label" for="q">Calidad</label>
            <select id="q" :value="s.quality" :disabled="lossless" @change="save({ quality: ($event.target as HTMLSelectElement).value })">
              <option v-for="q in QUALITIES" :key="q.value" :value="q.value">{{ q.label }}</option>
            </select>
          </div>
          <div>
            <label class="label" for="conc">Simultáneas: {{ s.concurrency }}</label>
            <input id="conc" type="range" min="1" max="6" :value="s.concurrency" @change="onConcurrency" />
          </div>
        </div>
        <div v-if="s.format === 'opus'" class="hint" style="margin-top: 8px">OPUS NO SUENA EN EL REPRODUCTOR DE MAC (SAFARI). USA MP3 O M4A SI QUIERES ESCUCHARLO AQUÍ.</div>
        <div v-if="lossless" class="hint" style="margin-top: 8px">SIN PÉRDIDA: LA CALIDAD NO APLICA.</div>
      </div>

      <div class="panel-box">
        <div class="cap">Mascot</div>
        <div class="sub">Baila en el visualizador cuando suena la música y se queda en idle en pausa.</div>
        <div class="mascots">
          <button
            v-for="m in MASCOTS"
            :key="m.id"
            class="mascot"
            :class="{ on: s.character === m.id }"
            @click="save({ character: m.id })"
          >
            <img v-if="m.id !== 'none'" :src="`./sprites/${m.id}/idle/01.png`" alt="" />
            <span v-else class="nomascot">—</span>
            <b>{{ m.label }}</b>
          </button>
        </div>
      </div>

      <div class="panel-box">
        <div class="cap">Player</div>
        <label class="label" for="keyn" style="margin-top: 0">Notación de la tonalidad</label>
        <select id="keyn" :value="s.keyNotation" @change="save({ keyNotation: ($event.target as HTMLSelectElement).value as 'camelot' | 'note' })">
          <option value="camelot">Camelot (8A, 11B…)</option>
          <option value="note">Nota (A min, C maj…)</option>
        </select>
        <details class="diag">
          <summary>Diagnóstico de arranque</summary>
          <pre>{{ player.log.length ? player.log.join('\n') : 'Reproduce una canción y vuelve aquí.' }}</pre>
        </details>
      </div>

      <div class="panel-box">
        <div class="cap">YouTube</div>
        <div class="sub">Si YouTube pide «confirma que no eres un bot», deja que yt-dlp use las cookies de tu navegador (debes tener sesión abierta en él). Safari no es compatible: macOS bloquea la lectura de sus cookies.</div>
        <label class="label" for="cb">Navegador para las cookies</label>
        <select id="cb" :value="s.cookiesBrowser" @change="save({ cookiesBrowser: ($event.target as HTMLSelectElement).value })">
          <option value="">Ninguno</option>
          <option v-for="b in BROWSERS" :key="b.id" :value="b.id">{{ b.label }}</option>
        </select>
        <div v-if="s.cookiesBrowser === 'chrome'" class="hint" style="margin-top: 6px">CHROME EN MAC: PUEDE PEDIR LA CONTRASEÑA DEL LLAVERO LA PRIMERA VEZ.</div>
        <div v-if="st && !st.ytDlpManaged && st.ytDlpPath" class="hint" style="margin-top: 6px">TU yt-dlp ES LA DEL SISTEMA (NO SE ACTUALIZA SOLA). PULSA «INSTALAR COPIA PROPIA» EN TOOLS: YOUTUBE CAMBIA SEGUIDO.</div>
      </div>

      <div class="panel-box">
        <div class="cap">Tools</div>
        <div class="tool">
          <div>
            <div class="title">yt-dlp</div>
            <div v-if="st?.ytDlpPath" class="sub">
              Versión {{ st.ytDlpVersion || '…' }} · {{ st.ytDlpManaged ? 'copia propia de la app' : 'instalada en el sistema' }}
            </div>
            <div v-else class="error">No encontrado todavía.</div>
          </div>
          <button class="btn" :disabled="st?.busy" @click="updateTools">
            {{ st?.busy ? 'Trabajando…' : st?.ytDlpManaged ? 'Actualizar' : 'Instalar copia propia' }}
          </button>
        </div>
        <label class="check">
          <input type="checkbox" :checked="s.autoUpdate" @change="save({ autoUpdate: ($event.target as HTMLInputElement).checked })" />
          Actualizar yt-dlp al abrir (recomendado: los sitios cambian seguido)
        </label>

        <div class="tool">
          <div>
            <div class="title">ffmpeg</div>
            <div v-if="st?.ffmpegOk" class="sub">{{ st.ffmpegPath }}</div>
            <div v-else class="error">No encontrado. Instálalo con <code>{{ ffmpegHint }}</code> o indica dónde está.</div>
          </div>
        </div>
        <div class="row">
          <input
            class="grow"
            type="text"
            :value="s.ffmpegPath"
            placeholder="Ruta manual a ffmpeg (opcional)"
            @change="save({ ffmpegPath: ($event.target as HTMLInputElement).value })"
          />
          <button class="btn" @click="chooseFFmpeg">Elegir…</button>
          <button v-if="s.ffmpegPath" class="btn" @click="save({ ffmpegPath: '' })">Auto</button>
        </div>
      </div>
    </div>

    <div class="panel-box">
      <div class="cap">Spotify <span class="tag" v-if="s.spotifyUser">{{ s.spotifyUser }}</span></div>
      <div class="sub">Cada quien usa su propia app de Spotify (gratis en developer.spotify.com). Las credenciales se guardan solo en tu computadora.</div>

      <label class="label">Client ID</label>
      <input type="text" :value="s.spotifyClientId" spellcheck="false" autocomplete="off" @change="save({ spotifyClientId: ($event.target as HTMLInputElement).value })" />
      <label class="label">Client Secret</label>
      <input type="password" :value="s.spotifyClientSecret" spellcheck="false" autocomplete="off" @change="save({ spotifyClientSecret: ($event.target as HTMLInputElement).value })" />

      <label class="label">Redirect URI (pégala tal cual en tu app de Spotify)</label>
      <div class="row">
        <div class="kv grow">{{ redirect }}</div>
        <button class="btn" @click="copyRedirect">Copiar</button>
      </div>

      <div class="row" style="margin-top: 12px">
        <button class="btn" :disabled="spBusy || !s.spotifyClientId || !s.spotifyClientSecret" @click="spotifyTest">Test</button>
        <button v-if="!s.spotifyUser" class="btn primary" :disabled="spBusy || !s.spotifyClientId" @click="spotifyLogin">
          {{ spBusy ? 'Esperando…' : 'Login' }}
        </button>
        <button v-else class="btn danger" @click="spotifyLogout">Logout</button>
      </div>

      <ul class="notes">
        <li>Canciones y álbumes funcionan solo con Client ID + Secret.</li>
        <li>Tus playlists y «Me gusta» requieren <b>Login</b> (se abre el navegador una vez).</li>
        <li>Spotify exige que el dueño de la app tenga Premium para usar su API.</li>
        <li>El audio no sale de Spotify: se busca en YouTube y solo se baja si coincide título, artista y duración.</li>
      </ul>
    </div>
  </section>
</template>
