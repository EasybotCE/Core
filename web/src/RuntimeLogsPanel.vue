<script setup lang="ts">
import { Activity, Check, Search, X } from '@lucide/vue'
import type { LogEntry, LogStatus } from './models'

const props = defineProps<{
  loading: boolean
  error: string
  logs: LogEntry[]
  logsTotal: number
  logsOffset: number
  logStatus: LogStatus | null
  savingLogSettings: boolean
}>()

const query = defineModel<string>('query', { required: true })
const level = defineModel<string>('level', { required: true })
const component = defineModel<string>('component', { required: true })
const requestId = defineModel<string>('requestId', { required: true })
const from = defineModel<string>('from', { required: true })
const to = defineModel<string>('to', { required: true })
const persistence = defineModel<boolean>('persistence', { required: true })
const retentionDays = defineModel<number>('retentionDays', { required: true })
const maxRows = defineModel<number>('maxRows', { required: true })

const emit = defineEmits<{
  refresh: []
  filter: []
  page: [offset: number]
  saveSettings: []
  clearError: []
}>()
</script>

<template>
  <section>
    <div class="mb-4 flex flex-wrap items-end justify-between gap-3"><div><div class="mb-1.5 text-[13px] text-[#89959a]">系统</div><h1 class="text-2xl font-semibold tracking-normal">运行日志</h1><p class="mt-1 text-[13px] text-[#7d898e]">{{ logStatus?.persistent ? '持久化已启用' : '仅保留内存日志' }} · 丢弃 {{ logStatus?.droppedWrites ?? 0 }} / 写入失败 {{ logStatus?.failedWrites ?? 0 }}</p></div><button class="btn-secondary" @click="emit('refresh')"><Activity :size="15" />刷新</button></div>
    <div class="mb-4 flex flex-wrap items-end gap-3 border-y border-[#e6ebec] bg-white p-3">
      <label class="flex h-9 items-center gap-2 text-[13px]"><input v-model="persistence" type="checkbox" class="accent-[#315f4a]" />持久化日志</label>
      <label class="field gap-1!">保留天数<input v-model.number="retentionDays" type="number" min="1" max="3650" class="h-9! w-28!" /></label>
      <label class="field gap-1!">最大记录数<input v-model.number="maxRows" type="number" min="1000" max="10000000" step="1000" class="h-9! w-36!" /></label>
      <button class="btn-primary compact ml-auto" :disabled="savingLogSettings" @click="emit('saveSettings')"><Check :size="14" />{{ savingLogSettings ? '保存中…' : '保存策略' }}</button>
    </div>
    <div class="mb-3 flex flex-wrap items-center gap-2 border-y border-[#e6ebec] bg-white p-3">
      <input v-model="query" class="h-9 min-w-45 flex-1 rounded border border-[#e1e7e8] px-2.5 text-[13px] outline-none focus:border-[#739884]" placeholder="搜索日志内容" @keyup.enter="emit('filter')" />
      <select v-model="level" class="h-9 rounded border border-[#e1e7e8] bg-white px-2 text-[13px]" aria-label="日志级别"><option value="">全部级别</option><option value="error">错误</option><option value="warn">警告</option><option value="info">信息</option><option value="debug">调试</option></select>
      <input v-model="component" class="h-9 w-32 rounded border border-[#e1e7e8] px-2 text-[13px] outline-none" placeholder="模块" />
      <input v-model="requestId" class="h-9 w-40 rounded border border-[#e1e7e8] px-2 text-[13px] outline-none" placeholder="Request ID" />
      <label class="flex h-9 items-center gap-1.5 rounded border border-[#e1e7e8] px-2 text-[12px] text-[#879398]">从<input v-model="from" type="datetime-local" class="w-39.5 text-[12px] text-[#48565d] outline-none" /></label>
      <label class="flex h-9 items-center gap-1.5 rounded border border-[#e1e7e8] px-2 text-[12px] text-[#879398]">至<input v-model="to" type="datetime-local" class="w-39.5 text-[12px] text-[#48565d] outline-none" /></label>
      <button class="btn-secondary compact" @click="emit('filter')"><Search :size="14" />筛选</button>
    </div>
    <div v-if="error" class="mb-3 flex items-center gap-2 rounded border border-[#f0d8d3] bg-[#fcf0ee] p-2 text-[13px] text-[#a94f45]"><X :size="15" />{{ error }}<button class="ml-auto" @click="emit('clearError')"><X :size="14" /></button></div>
    <div v-if="loading" class="empty">正在读取日志…</div>
    <div v-else-if="logs.length" class="overflow-x-auto rounded-md border border-[#e4e9ea] bg-white"><table class="w-full min-w-160 border-collapse text-left"><thead class="bg-[#fafbfb] text-[13px] text-[#879398]"><tr><th>时间</th><th>级别</th><th>来源</th><th>事件</th></tr></thead><tbody><tr v-for="(item, index) in logs" :key="`${item.time}-${index}`" class="border-t border-[#edf0f1]"><td class="whitespace-nowrap text-[13px] text-[#89959a]">{{ new Date(item.time).toLocaleString('zh-CN') }}</td><td><span class="log-level" :class="`level-${item.level}`">{{ item.level.toUpperCase() }}</span></td><td class="text-[13px] text-[#718087]">{{ item.source }}</td><td class="font-mono text-[13px] text-[#48565c]">{{ item.message }}</td></tr></tbody></table></div>
    <div v-else class="empty"><b>暂无运行日志</b><p>API 操作和服务生命周期事件会显示在这里。</p></div>
    <div class="mt-3 flex items-center justify-between text-[13px] text-[#7d898e]"><span>共 {{ logsTotal.toLocaleString() }} 条</span><div class="flex gap-2"><button class="btn-secondary compact" :disabled="logsOffset === 0" @click="emit('page', Math.max(0, logsOffset - 100))">上一页</button><button class="btn-secondary compact" :disabled="logsOffset + logs.length >= logsTotal" @click="emit('page', logsOffset + 100)">下一页</button></div></div>
    <p class="mt-3 text-[12px] text-[#9ba5a8]">日志正文不会默认记录。关闭持久化后，新事件仅留在内存尾部，既有日志仍按保留策略清理。</p>
  </section>
</template>
