<script setup lang="ts">
import { api } from '../api'
import { formatEta, formatSpeed } from '../format'
import { activeCount, failedCount, finishedCount, guarded, state } from '../store'
import type { Job, JobStatus } from '../types'
import { ACTIVE } from '../types'

const LABELS: Record<JobStatus, string> = {
  queued: 'En cola',
  downloading: 'Descargando',
  converting: 'Convirtiendo',
  done: 'Listo',
  failed: 'Error',
  canceled: 'Cancelada',
}

const isActive = (j: Job) => ACTIVE.includes(j.status)
const showBar = (j: Job) => j.status === 'downloading' || j.status === 'converting'

function detail(j: Job): string {
  if (j.status !== 'downloading') return ''
  return [`${Math.round(j.percent)}%`, formatSpeed(j.speed), formatEta(j.eta) && `quedan ${formatEta(j.eta)}`]
    .filter(Boolean)
    .join(' · ')
}

const retry = (id: string) => guarded(() => api.retryJob(id))
const cancel = (id: string) => guarded(() => api.cancelJob(id))
const remove = (id: string) => guarded(() => api.removeJob(id))
const reveal = (path: string) => guarded(() => api.revealFile(path))
const retryFailed = () => guarded(() => api.retryFailed())
const clearFinished = () => guarded(() => api.clearFinished())
const openFolder = () => guarded(() => api.openDownloads())
</script>

<template>
  <section class="panel-box">
    <div class="cap">Queue</div>
    <header class="row spread">
      <div class="sub">
        {{ activeCount }} en curso o en cola · {{ finishedCount }} terminadas
      </div>
      <div class="row">
        <button v-if="failedCount" class="btn small" @click="retryFailed">Reintentar fallidas ({{ failedCount }})</button>
        <button v-if="finishedCount" class="btn small" @click="clearFinished">Limpiar terminadas</button>
        <button class="btn small" @click="openFolder">Abrir carpeta</button>
      </div>
    </header>

    <div v-if="!state.jobs.length" class="empty">
      La cola está vacía.<br />
      Pega una URL en <b>Source</b> y añade canciones.
    </div>

    <ul class="jobs">
      <li v-for="j in state.jobs" :key="j.id" class="job" :class="j.status">
        <div class="job-main">
          <div class="title">
            <span v-if="j.index" class="num">{{ String(j.index).padStart(2, '0') }}</span>
            {{ j.title }}
          </div>
          <div class="sub">
            <span v-if="j.folder">{{ j.folder }}</span>
            <span v-if="j.folder && j.uploader"> · </span>
            <span v-if="j.uploader">{{ j.uploader }}</span>
          </div>
          <div v-if="showBar(j)" class="bar" :class="{ indeterminate: j.status === 'converting' }">
            <div class="bar-fill" :style="{ width: j.percent + '%' }" />
          </div>
          <div v-if="detail(j)" class="sub">{{ detail(j) }}</div>
          <div v-if="j.status === 'failed'" class="error">{{ j.error }}</div>
        </div>
        <div class="job-side">
          <span class="chip" :class="j.status">{{ LABELS[j.status] }}</span>
          <div class="row">
            <button v-if="j.status === 'done'" class="btn small" @click="reveal(j.filePath)">Mostrar</button>
            <button v-if="j.status === 'failed' || j.status === 'canceled'" class="btn small" @click="retry(j.id)">Reintentar</button>
            <button v-if="isActive(j)" class="btn small danger" @click="cancel(j.id)">Cancelar</button>
            <button v-else class="btn small ghost" title="Quitar de la lista" @click="remove(j.id)">✕</button>
          </div>
        </div>
      </li>
    </ul>
  </section>
</template>
