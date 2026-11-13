<script setup lang="ts">
import { computed, ref, watch } from 'vue'

type Option = { value: string; label: string }
type Field = {
  key: string
  label: string
  type: 'text' | 'number' | 'password' | 'select' | 'bool' | 'textarea'
  placeholder?: string
  hint?: string
  readonly?: boolean
  options?: Option[]
}

const props = defineProps<{
  kind: string
  initial: string
  serverOptions?: Option[]
  adapterOptions?: Option[]
}>()

const emit = defineEmits<{ change: [detail: string] }>()

const advanced = ref(false)
const text = ref(props.initial)
const values = ref<Record<string, unknown>>(parse(props.initial))

function parse(raw: string): Record<string, unknown> {
  try {
    const value = JSON.parse(raw || '{}')
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

const schemas: Record<string, Field[]> = {
  adapters: [
    { key: 'url', label: 'OneBot 连接地址', type: 'text', placeholder: 'ws://127.0.0.1:3001/', hint: '正向 WebSocket 地址，必须以 ws:// 或 wss:// 开头' },
    { key: 'accessToken', label: 'AccessToken', type: 'password', hint: '留空表示沿用已保存的令牌，不会回显明文' },
    { key: 'useHeartBeat', label: '启用心跳超时检测', type: 'bool' },
    { key: 'heartBeatInterval', label: '心跳间隔（毫秒）', type: 'number', placeholder: '6000' },
  ],
  servers: [
    { key: 'host', label: '服务器地址', type: 'text', placeholder: '127.0.0.1', hint: '桥接令牌由主程序生成，显示在弹窗顶部' },
    { key: 'port', label: '端口', type: 'number', placeholder: '25565' },
  ],
  'sync-rules': [
    { key: 'serverId', label: 'Minecraft 服务器', type: 'select', options: props.serverOptions ?? [] },
    { key: 'adapterId', label: '社交适配器', type: 'select', options: props.adapterOptions ?? [] },
    { key: 'groupId', label: '群号', type: 'text', placeholder: '123456789', hint: '机器人所在群的平台群号' },
    {
      key: 'direction', label: '同步方向', type: 'select', options: [
        { value: 'both', label: '双向同步' },
        { value: 'mc2social', label: '仅 游戏 → 群' },
        { value: 'social2mc', label: '仅 群 → 游戏' },
      ],
      hint: '双向模式会自动抑制回显，不会形成消息回环',
    },
    { key: 'mcFormat', label: '游戏 → 群 模板', type: 'text', placeholder: '[{server}] {player}: {content}', hint: '可用变量：{server} {player} {content}' },
    { key: 'socialFormat', label: '群 → 游戏 模板', type: 'text', placeholder: '[{group}] {sender}: {content}', hint: '可用变量：{group} {sender} {content}' },
  ],
}

const fields = computed(() => schemas[props.kind] ?? [])

watch(() => props.initial, (next) => {
  values.value = parse(next)
  text.value = next
})

watch(values, () => {
  if (advanced.value) {
    return
  }
  const output: Record<string, unknown> = {}
  for (const field of fields.value) {
    const raw = values.value[field.key]
    if (raw === undefined || raw === null || raw === '') {
      continue
    }
    output[field.key] = field.type === 'number' ? Number(raw) : raw
  }
  // Keep keys this form does not know about so a future field is never dropped
  // just because the console has not caught up yet.
  const known = new Set(fields.value.map(field => field.key))
  for (const [key, value] of Object.entries(values.value)) {
    if (!known.has(key)) {
      output[key] = value
    }
  }
  emit('change', JSON.stringify(output, null, 2))
}, { deep: true })

watch(text, (next) => {
  if (!advanced.value) {
    return
  }
  try {
    JSON.parse(next || '{}')
  } catch {
    return
  }
  emit('change', next)
})

function toggleAdvanced() {
  if (!advanced.value) {
    text.value = JSON.stringify(values.value, null, 2)
  } else {
    values.value = parse(text.value)
  }
  advanced.value = !advanced.value
}

function selectValue(field: Field): string {
  const value = values.value[field.key]
  return value === undefined || value === null ? '' : String(value)
}

function setSelect(field: Field, event: Event) {
  values.value[field.key] = (event.target as HTMLSelectElement).value
}

function setText(field: Field, event: Event) {
  values.value[field.key] = (event.target as HTMLInputElement).value
}

function setBool(field: Field, event: Event) {
  values.value[field.key] = (event.target as HTMLInputElement).checked
}

function textValue(field: Field): string {
  const value = values.value[field.key]
  return value === undefined || value === null ? '' : String(value)
}

function placeholderFor(field: Field): string {
  if (field.key === 'token' && !textValue(field)) {
    return '保存后自动生成'
  }
  return field.placeholder ?? ''
}
</script>

<template>
  <div class="config-form">
    <template v-if="fields.length">
      <div class="config-form-head">
        <span>{{ advanced ? 'JSON 模式' : '结构化配置' }}</span>
        <button type="button" class="text-link" @click="toggleAdvanced">{{ advanced ? '返回表单' : '高级：编辑 JSON' }}</button>
      </div>

      <template v-if="!advanced">
        <label v-for="field in fields" :key="field.key" class="field">
          <span>{{ field.label }}</span>

          <select v-if="field.type === 'select'" :value="selectValue(field)" @change="setSelect(field, $event)">
            <option value="">请选择…</option>
            <option v-for="option in field.options ?? []" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>

          <input
            v-else-if="field.type === 'bool'"
            type="checkbox"
            class="config-check"
            :checked="Boolean(values[field.key])"
            @change="setBool(field, $event)"
          />

          <input
            v-else
            :type="field.type === 'number' ? 'number' : field.type"
            :value="textValue(field)"
            :readonly="field.readonly"
            :placeholder="placeholderFor(field)"
            :class="{ readonly: field.readonly }"
            @input="setText(field, $event)"
          />

          <small v-if="field.hint">{{ field.hint }}</small>
        </label>
      </template>

      <label v-else class="field">
        <textarea v-model="text" rows="10" spellcheck="false"></textarea>
        <small>JSON 会与上方表单互相转换；保存前需要是合法 JSON。</small>
      </label>
    </template>

    <label v-else class="field">
      <textarea v-model="text" rows="8" spellcheck="false" @input="emit('change', text)"></textarea>
      <small>该配置类型暂时没有结构化表单，可直接编辑 JSON。</small>
    </label>
  </div>
</template>
