<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Activity, RefreshCw, Trash2 } from '@lucide/vue'

type Binding = { id: string; playerUuid: string; serverId?: string; platformType: string; accountId: string; displayName?: string; boundAt: string }
type Audit = { action: string; playerUuid: string; platformType: string; accountId: string; occurredAt: string }
const props = defineProps<{ api: <T>(path: string, init?: RequestInit) => Promise<T> }>()
const bindings = ref<Binding[]>([])
const audit = ref<Audit[]>([])
const busy = ref(false)
const error = ref('')
const notice = ref('')

async function load() {
  busy.value = true
  error.value = ''
  try {
    ;[bindings.value, audit.value] = await Promise.all([
      props.api<Binding[]>('/api/v1/bindings'),
      props.api<Audit[]>('/api/v1/bindings/audit?limit=50'),
    ])
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '读取绑定数据失败'
  } finally {
    busy.value = false
  }
}

async function remove(binding: Binding) {
  if (!window.confirm(`解除玩家 ${binding.playerUuid} 与平台账号 ${binding.accountId} 的绑定？`)) return
  error.value = ''
  try {
    await props.api(`/api/v1/bindings/${encodeURIComponent(binding.id)}`, { method: 'DELETE' })
    notice.value = '绑定关系已解除'
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '解绑失败'
  }
}

onMounted(load)
</script>

<template>
  <section class="binding-workspace">
    <header class="binding-header">
      <div><span class="binding-kicker">PLAYER IDENTITY</span><h1>玩家与绑定</h1><p>验证码由强制绑定服务器的玩家进服流程生成，玩家在群内发送“绑定 验证码”完成验证。</p></div>
      <button class="binding-refresh" type="button" :disabled="busy" @click="load"><RefreshCw :size="15" />刷新</button>
    </header>
    <p v-if="error" class="binding-alert error" role="alert">{{ error }}</p>
    <p v-if="notice" class="binding-alert success" role="status">{{ notice }}</p>
    <section class="binding-card">
      <div class="binding-card-title"><span class="binding-icon mint"><Activity :size="17" /></span><div><h2>当前绑定关系</h2><p>一个平台账号最多关联 4 名游戏玩家；每名玩家在单个平台只能关联一个账号。共 {{ bindings.length }} 条</p></div></div>
      <div v-if="busy && !bindings.length" class="binding-empty">正在加载…</div>
      <div v-else-if="bindings.length" class="binding-list">
        <article v-for="item in bindings" :key="item.id">
          <div><b>{{ item.displayName || item.accountId }}</b><span>{{ item.platformType }} · {{ item.accountId }}</span><code>{{ item.playerUuid }}</code></div>
          <button type="button" :aria-label="`解除玩家 ${item.playerUuid} 的绑定`" class="binding-delete" @click="remove(item)"><Trash2 :size="15" /></button>
        </article>
      </div>
      <div v-else class="binding-empty">暂时没有绑定记录</div>
    </section>
    <section class="binding-card audit-card">
      <div class="binding-card-title"><span class="binding-icon amber"><Activity :size="17" /></span><div><h2>最近审计</h2><p>绑定与管理员解绑操作记录</p></div></div>
      <div v-if="audit.length" class="audit-list"><div v-for="(item,index) in audit" :key="`${item.occurredAt}-${index}`"><b>{{ item.action === 'bind' ? '完成绑定' : '解除绑定' }}</b><code>{{ item.playerUuid }}</code><span>{{ item.platformType }} · {{ item.accountId }}</span><time>{{ new Date(item.occurredAt).toLocaleString('zh-CN') }}</time></div></div>
      <div v-else class="binding-empty">暂无审计事件</div>
    </section>
  </section>
</template>

<style scoped>
.binding-workspace{display:grid;gap:18px;color:#334149}.binding-header{display:flex;justify-content:space-between;align-items:center}.binding-kicker{font-size:10px;font-weight:700;letter-spacing:.18em;color:#6d8a79}.binding-header h1{margin:5px 0;font-size:24px;font-weight:650}.binding-header p,.binding-card-title p{margin:0;color:#849095;font-size:12px}.binding-refresh{display:flex;align-items:center;gap:7px;border:1px solid #dce5e1;border-radius:4px;background:#fff;padding:9px 12px;color:#41634f;font-size:12px}.binding-refresh:disabled{opacity:.55}.binding-card{min-width:0;border:1px solid #e4e9e7;border-radius:5px;background:#fff;padding:17px}.binding-card-title{display:flex;gap:10px;align-items:center;margin-bottom:18px}.binding-card-title h2{margin:0 0 4px;font-size:14px;font-weight:650}.binding-icon{display:grid;width:34px;height:34px;place-items:center;border-radius:5px;background:#edf3f0;color:#537760}.binding-icon.mint{background:#e9f4ef}.binding-icon.amber{background:#f7f1e6;color:#9a7540}.binding-list article{display:flex;justify-content:space-between;align-items:center;padding:11px 0;border-top:1px solid #edf0ef}.binding-list article div{display:grid;gap:4px;min-width:0}.binding-list b{font-size:12px}.binding-list span,.binding-list code{font-size:10px;color:#849095}.binding-list code{overflow-wrap:anywhere}.binding-delete{border:0;background:none;color:#aa685b;cursor:pointer}.binding-empty{padding:30px 8px;text-align:center;font-size:12px;color:#9aa3a4}.audit-card{padding-bottom:8px}.audit-list>div{display:grid;grid-template-columns:100px minmax(120px,1fr) 1fr 170px;gap:12px;align-items:center;padding:10px 0;border-top:1px solid #edf0ef;font-size:11px}.audit-list code,.audit-list span,.audit-list time{color:#839095;overflow-wrap:anywhere}.binding-alert{margin:0;padding:10px 12px;border-radius:4px;font-size:12px}.binding-alert.error{background:#fcf0ee;color:#a94f45}.binding-alert.success{background:#eff7f1;color:#4f785a}@media(max-width:800px){.audit-list>div{grid-template-columns:1fr 1fr}.binding-header{align-items:flex-start}}
</style>
