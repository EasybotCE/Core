<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '../api'
type Report = { conflict: boolean; matched: number; allowed: number; invalid: number; disabled: number; items: { id: string; name: string; error?: string; result?: { matched: boolean; allowed: boolean; reason: string; reply?: string } }[] }
const props = defineProps<{ revision: string }>()
const message = ref('/help'), verified = ref(false), role = ref('member'), bound = ref(false), identities = ref(''), busy = ref(false), error = ref(''), report = ref<Report | null>(null)
let revision = 0
watch([message, verified, role, bound, identities, () => props.revision], () => { revision++; report.value = null; error.value = ''; busy.value = false })
async function diagnose() {
  const current = ++revision
  busy.value = true; error.value = ''; report.value = null
  try {
    const result = await api<Report>('/api/commands/diagnose', { method: 'POST', body: JSON.stringify({ message: message.value, context: { userId: 'diagnostic-user', senderName: '诊断用户', verified: verified.value, role: role.value, bound: bound.value, identities: identities.value.split('\n').filter(Boolean) } }) })
    if (current === revision) report.value = result
  } catch (e) { if (current === revision) error.value = e instanceof Error ? e.message : '诊断失败' }
  finally { if (current === revision) busy.value = false }
}
const reasons: Record<string, string> = { not_matched: '未匹配', permission_denied: '匹配但权限不足', preview_ready: '可生成回复', reply_too_long: '回复展开超限' }
</script>

<template>
  <section class="mb-5 rounded border border-[#e4e9ea] bg-white p-4">
    <h2 class="mb-2 text-[15px] font-semibold">多命令冲突诊断</h2>
    <p class="mb-3 text-[13px] text-[#7d898e]">检查全部已启用的已保存命令。身份是假设输入；不发送消息，不选择执行顺序。最多 500 条命令。</p>
    <div class="flex flex-col gap-3">
      <label class="field">测试消息<input v-model="message" maxlength="8192" /></label>
      <label class="flex justify-between text-[13px]">假设已验证身份<input v-model="verified" type="checkbox" /></label>
      <label class="field">假设角色<select v-model="role"><option value="member">普通成员</option><option value="admin">管理员</option><option value="owner">群主</option><option value="unknown">未知</option></select></label>
      <label class="flex justify-between text-[13px]">假设已绑定<input v-model="bound" type="checkbox" /></label>
      <label class="field">假设身份（每行一个）<textarea v-model="identities" rows="2" /></label>
      <button class="btn-secondary" type="button" :disabled="busy" @click="diagnose">{{ busy ? '诊断中…' : '诊断已保存命令' }}</button>
      <p v-if="error" role="alert" class="text-[13px] text-[#a94f45]">{{ error }}</p>
      <div v-if="report" aria-live="polite" class="text-[13px]">
        <p :class="report.conflict ? 'text-[#a94f45]' : ''">{{ report.conflict ? '发现多命令匹配冲突，请调整关键词或正则。' : '未发现多命令匹配冲突。' }}</p>
        <p>匹配 {{ report.matched }} · 假设授权 {{ report.allowed }} · 无效配置 {{ report.invalid }} · 跳过停用 {{ report.disabled }} · 实际执行 0</p>
        <ul class="mt-2 divide-y divide-[#edf0f1]"><li v-for="item in report.items" :key="item.id" class="py-2"><b>{{ item.name }}</b> · {{ item.error || reasons[item.result?.reason ?? ''] || item.result?.reason }}<pre v-if="item.result?.reply" class="whitespace-pre-wrap break-words">{{ item.result.reply }}</pre></li></ul>
        <p v-if="!report.items.length">没有可诊断的已启用命令。</p>
      </div>
    </div>
  </section>
</template>
