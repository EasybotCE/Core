export type Kind = 'adapters' | 'servers' | 'players' | 'social-accounts' | 'groups' | 'whitelist' | 'blacklist' | 'commands' | 'ai-providers' | 'image-templates' | 'plugins' | 'settings' | 'sync-rules' | 'chat-images' | 'placeholders' | 'player-statistics' | 'skins'
export type Entry = { id: string; kind: Kind; name: string; detail: string; enabled: boolean; createdAt: string; updatedAt: string }
export type LogEntry = { id: string; time: string; level: string; source: string; message: string; requestId?: string }
export type LogPage = { items: LogEntry[]; total: number; limit: number; offset: number }
export type LogStatus = { persistent: boolean; retentionDays: number; maxRows: number; droppedWrites: number; failedWrites: number }
export type AdapterStatus = { state: 'stopped' | 'starting' | 'connected' | 'reconnecting' | 'stopping' | 'failed'; lastError?: string; reconnectCount: number; selfId?: string; nickname?: string }
export type BridgeStatus = { serverId: string; serverName: string; state: 'offline' | 'online'; pluginVersion?: string; onlinePlayers: number; lastSeenAt?: string }
