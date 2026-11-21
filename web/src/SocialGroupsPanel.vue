<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Entry } from './models'
import { Activity, Radio, RefreshCw, Users } from '@lucide/vue'

type SocialGroup = Record<string, unknown>
type Api = <T>(path: string, init?: RequestInit) => Promise<T>

const props = defineProps<{ adapters: Entry[]; api: Api }>()
const adapterId = ref('')
const groups = ref<SocialGroup[]>([])
const loading = ref(false)
const error = ref('')
const selectedAdapter = computed(() => props.adapters.find(item => item.id === adapterId.value))
const groupApiName = computed(() => {
  try {
    const detail = JSON.parse(selectedAdapter.value?.detail || '{}')
    return detail.adapterType === 'QQBot' ? 'QQ Bot /users/@me/guilds API' : 'OneBot 11 get_group_list API'
  } catch {
    return '平台群组 API'
  }
})

function field(group: SocialGroup, ...keys: string[]) {
  for (const key of keys) {
    const value = group[key]
    if (value !== undefined && value !== null && value !== '') return String(value)
  }
  return ''
}

async function refresh() {
  if (!adapterId.value) {
    groups.value = []
    return
  }
  loading.value = true
  error.value = ''
  try {
    groups.value = await props.api<SocialGroup[]>(`/api/v1/adapter/${encodeURIComponent(adapterId.value)}/groups`)
  } catch (e) {
    groups.value = []
    error.value = e instanceof Error ? e.message : '无法读取平台群组'
  } finally {
    loading.value = false
  }
}

watch(() => props.adapters, (items) => {
  if (!items.some(item => item.id === adapterId.value)) adapterId.value = items[0]?.id ?? ''
}, { immediate: true })
watch(adapterId, () => { void refresh() })
</script>

<template>
  <section class="social-groups-page">
    <header class="groups-intro">
      <div>
        <span class="eyebrow">消息 · 平台目录</span>
        <h1>群组</h1>
        <p>群组列表来自当前适配器，不在控制台中手动创建或编辑。</p>
      </div>
      <span class="groups-count"><b>{{ groups.length }}</b><small>个群组</small></span>
    </header>

    <div class="groups-toolbar">
      <label>
        <span>读取来源</span>
        <select v-model="adapterId" :disabled="!props.adapters.length">
          <option v-for="adapter in props.adapters" :key="adapter.id" :value="adapter.id">{{ adapter.name }}</option>
        </select>
      </label>
      <button class="btn-secondary compact" :disabled="loading || !adapterId" @click="refresh">
        <RefreshCw :size="14" :class="loading ? 'animate-spin' : ''" />重新读取
      </button>
      <small v-if="selectedAdapter" class="source-hint"><Activity :size="13" />读取自 {{ selectedAdapter.name }} 的 {{ groupApiName }}</small>
    </div>

    <div v-if="error" class="groups-error" role="alert">{{ error }}</div>
    <div v-else-if="loading" class="groups-loading">正在向适配器读取群组…</div>
    <div v-else-if="!props.adapters.length" class="groups-empty">
      <Radio :size="22" />
      <b>尚未配置平台适配器</b>
      <p>添加并启动平台适配器后，群组会在此处按平台 API 自动读取。</p>
    </div>
    <div v-else-if="!groups.length" class="groups-empty">
      <Users :size="22" />
      <b>没有可显示的群组</b>
      <p>请确认适配器已连接，并允许机器人读取群列表。</p>
    </div>
    <div v-else class="groups-grid">
      <article v-for="(group, index) in groups" :key="field(group, 'group_id', 'groupId', 'id') || index" class="group-card">
        <span class="group-avatar"><Users :size="17" /></span>
        <div class="group-details">
          <b>{{ field(group, 'group_name', 'groupName', 'name') || '未命名群组' }}</b>
          <code>{{ field(group, 'group_id', 'groupId', 'id') || '无群组 ID' }}</code>
        </div>
        <span v-if="field(group, 'member_count', 'memberCount')" class="group-members">{{ field(group, 'member_count', 'memberCount') }} 人</span>
      </article>
    </div>
  </section>
</template>
