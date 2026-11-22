<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'
import { navigation, navigationGroups } from './navigation'

withDefaults(defineProps<{
  selected: string
  counts: Record<string, number>
  apiOnline: boolean
}>(), {})

const emit = defineEmits<{ select: [id: string] }>()
</script>

<template>
  <aside class="sidebar fixed inset-y-0 left-0 z-10 flex w-62 flex-col px-3 py-5 text-[#d8e0e2]">
    <div class="brand mb-6 flex items-center gap-2.5 px-2"><img src="/brand.svg" alt="EasyBotCE" class="h-8 w-8 shrink-0 rounded-md" /><span class="flex flex-col"><b class="text-[15px] text-white">EasyBotCE</b><small class="text-[11px] text-[#96a7ad]">社区管理控制台</small></span><span class="ml-auto rounded-sm bg-[#31424b] px-1.5 py-0.5 text-[11px] text-[#aebbc0]">DEV</span></div>
    <div class="instance-switch mb-4 flex items-center gap-2 rounded-md p-2"><span class="flex flex-1 flex-col"><b class="text-[13px] text-white">本地实例</b><small class="text-[11px] text-[#99a7ac]">管理控制台</small></span><ChevronDown :size="14" /></div>
    <nav class="flex-1 overflow-auto">
      <template v-for="group in navigationGroups" :key="group">
        <p class="px-2 pb-1 pt-4 text-[13px] text-[#84949a]">{{ group }}</p>
        <button v-for="item in navigation.filter(entry => entry.group === group)" :key="item.id" class="nav-link flex h-8.5 w-full items-center gap-2.5 rounded px-2 text-left text-[13px] text-[#bac5c8] hover:bg-[#243740] hover:text-white" :class="{ chosen: selected === item.id }" :title="item.label" :aria-label="item.label" :aria-current="selected === item.id ? 'page' : undefined" @click="emit('select', item.id)">
          <component :is="item.icon" :size="16" /><span>{{ item.label }}</span><small v-if="item.kind === 'adapters' && counts.adapters" class="ml-auto">{{ counts.adapters }}</small>
        </button>
      </template>
    </nav>
    <div class="sidebar-footer flex items-center gap-2 px-2 pt-3 text-[12px]"><i class="size-1.5 rounded-full" :class="apiOnline ? 'bg-[#45bd85]' : 'bg-[#d28564]'" />{{ apiOnline ? '服务在线' : '服务离线' }}<span class="ml-auto text-[11px] text-[#829198]">0.1.0-dev</span></div>
  </aside>
</template>
