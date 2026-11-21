import { Activity, Command, Gamepad2, LayoutDashboard, ListChecks, MessageSquareText, Radio, Shield, Users } from '@lucide/vue'
import type { Component } from 'vue'
import type { Kind } from './models'

export type NavigationItem = { id: string; label: string; icon: Component; group: string; kind?: Kind }

export const navigation: NavigationItem[] = [
  { id: 'overview', label: '概览', icon: LayoutDashboard, group: '概览' },
  { id: 'adapters', label: '平台适配器', icon: Radio, group: '接入', kind: 'adapters' },
  { id: 'servers', label: 'Minecraft 服务器', icon: Gamepad2, group: '接入', kind: 'servers' },
  { id: 'groups', label: '群组', icon: MessageSquareText, group: '消息', kind: 'groups' },
  { id: 'players', label: '玩家与绑定', icon: Users, group: '玩家', kind: 'players' },
  { id: 'whitelist', label: '白名单', icon: ListChecks, group: '玩家', kind: 'whitelist' },
  { id: 'blacklist', label: '黑名单', icon: Shield, group: '玩家', kind: 'blacklist' },
  { id: 'commands', label: '命令', icon: Command, group: '自动化', kind: 'commands' },
  { id: 'logs', label: '运行日志', icon: Activity, group: '运维' },
]

export const navigationGroups = ['概览', '接入', '消息', '玩家', '自动化', '运维']
