<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '../api'

const props = defineProps<{ modelValue: string; enabled: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
type Config = { schemaVersion: number; prefix: string; match: string; triggers: string[]; permission: string; identity: string; action: string; reply: string }
type Result = { matched: boolean; allowed: boolean; reason: string; arguments: string[]; reply?: string; executed: boolean }
const defaults = (): Config => ({ schemaVersion: 1, prefix: '/', match: 'exact', triggers: ['帮助', 'help'], permission: 'all', identity: '', action: 'text', reply: '你好，{sender_name}' })
const draft = ref<Config>(defaults()), legacy = ref(false), message = ref('/help'), sender = ref('测试用户'), userId = ref('test-user'), verified = ref(false), role = ref('member'), bound = ref(false), identities = ref(''), busy = ref(false), error = ref(''), result = ref<Result | null>(null)
let revision = 0
const reasons: Record<string, string> = { disabled: '命令已停用', not_matched: '未匹配', permission_denied: '权限不足', reply_too_long: '展开后的回复超过上限', preview_ready: '可生成预期回复' }
const resultReason = computed(() => result.value ? reasons[result.value.reason] ?? result.value.reason : '')
watch(() => props.modelValue, raw => {
  try {
    const value = JSON.parse(raw)
    legacy.value = value?.schemaVersion !== 1 || !Array.isArray(value.triggers) || !value.triggers.every((v: unknown) => typeof v === 'string') || !['prefix', 'match', 'permission', 'identity', 'action', 'reply'].every(key => typeof value[key] === 'string')
    draft.value = legacy.value ? defaults() : value
  } catch { legacy.value = true; draft.value = defaults() }
}, { immediate: true })
function commit() { emit('update:modelValue', JSON.stringify(draft.value, null, 2)) }
function replaceLegacy() { draft.value = defaults(); commit() }
function updateTriggers(event: Event) { draft.value.triggers = (event.target as HTMLTextAreaElement).value.split('\n'); commit() }
function permissionChanged() { if (draft.value.permission !== 'custom') draft.value.identity = ''; commit() }
watch([() => props.modelValue, () => props.enabled, message, sender, userId, verified, role, bound, identities], () => { revision++; result.value = null; error.value = ''; busy.value = false })
async function simulate() {
  const current = ++revision
  busy.value = true; error.value = ''; result.value = null
  try {
    const value = await api<Result>('/api/commands/simulate', { method: 'POST', body: JSON.stringify({ detail: props.modelValue, enabled: props.enabled, message: message.value, context: { userId: userId.value, senderName: sender.value, verified: verified.value, role: role.value, bound: bound.value, identities: identities.value.split('\n').filter(Boolean) } }) })
    if (current === revision) result.value = value
  } catch (e) { if (current === revision) error.value = e instanceof Error ? e.message : '模拟失败' }
  finally { if (current === revision) busy.value = false }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <p class="text-[13px] text-[#806c3e]">目前支持文本命令配置与模拟；保存或模拟均不会发送消息或执行命令。</p>
    <template v-if="legacy">
      <p class="text-[13px] text-[#a94f45]">此配置尚不能由版本 1 表单识别，原文保留。请先备份核对，再选择替换编辑草稿。</p>
      <label class="field">原配置 JSON<textarea :value="modelValue" rows="6" @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)" /></label>
      <button type="button" class="btn-secondary" @click="replaceLegacy">用版本 1 示例替换此编辑草稿</button>
    </template>
    <template v-else>
      <label class="field">触发前缀<input v-model="draft.prefix" maxlength="32" placeholder="可留空，例如 / 或 !" @input="commit" /></label>
      <label class="field">匹配方式<select v-model="draft.match" @change="commit"><option value="exact">精确匹配</option><option value="regex">正则匹配（RE2，完整消息匹配）</option></select></label>
      <label class="field">关键词或正则（每行一条，按顺序首个匹配）<textarea :value="draft.triggers.join('\n')" rows="3" @input="updateTriggers" /><small>精确匹配不接受额外参数。正则不包含前缀，捕获组最多 9 个。</small></label>
      <label class="field">权限<select v-model="draft.permission" @change="permissionChanged"><option value="all">全员</option><option value="admin">管理员或群主</option><option value="owner">群主</option><option value="bound">已绑定</option><option value="custom">自定义身份</option></select></label>
      <label v-if="draft.permission === 'custom'" class="field">身份标识<input v-model="draft.identity" maxlength="120" @input="commit" /></label>
      <label class="field">文本回复<textarea v-model="draft.reply" rows="3" @input="commit" /><small>变量：{sender_name}、{user_id}、{arg1} 至 {arg9}（须有对应捕获组）。</small></label>
    </template>
    <div class="mt-2 flex flex-col gap-3 border-t border-[#e4e9ea] pt-3">
      <b class="text-[14px]">模拟解析</b>
      <p class="text-[12px] text-[#806c3e]">以下身份均是假设输入，不会授予真实权限。尚未检查冷却、平台范围或命令之间的匹配冲突。</p>
      <label class="field">测试消息<input v-model="message" maxlength="8192" /></label>
      <div class="flex gap-2"><label class="field min-w-0 flex-1">用户 ID<input v-model="userId" maxlength="128" /></label><label class="field min-w-0 flex-1">显示名称<input v-model="sender" maxlength="512" /></label></div>
      <label class="flex justify-between text-[13px]">模拟已验证身份<input v-model="verified" type="checkbox" /></label>
      <label class="field">假设平台角色<select v-model="role"><option value="unknown">未知</option><option value="member">普通成员</option><option value="admin">管理员</option><option value="owner">群主</option></select></label>
      <label class="flex justify-between text-[13px]">假设已绑定<input v-model="bound" type="checkbox" /></label>
      <label class="field">假设自定义身份（每行一个）<textarea v-model="identities" rows="2" /></label>
      <button type="button" class="btn-secondary" :disabled="busy" @click="simulate">{{ busy ? '模拟中…' : '模拟当前草稿' }}</button>
      <p v-if="error" role="alert" class="text-[13px] text-[#a94f45]">{{ error }}</p>
      <div v-if="result" class="rounded bg-[#f3f6f7] p-3 text-[13px]" aria-live="polite"><b>{{ resultReason }}</b><p>匹配：{{ result.matched ? '是' : '否' }} · 假设授权：{{ result.allowed ? '允许' : '未允许' }} · 实际执行：否</p><p v-if="result.arguments.length" class="whitespace-pre-wrap">捕获参数：{{ JSON.stringify(result.arguments) }}</p><pre v-if="result.reply" class="mt-2 whitespace-pre-wrap break-words">{{ result.reply }}</pre></div>
    </div>
  </div>
</template>
