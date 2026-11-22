// Package model contains shared data contracts without infrastructure dependencies.
package model

import "time"

type Record struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Detail    string    `json:"detail"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var Kinds = map[string]bool{
	"adapters": true, "servers": true, "players": true, "social-accounts": true,
	"groups": true, "whitelist": true, "blacklist": true, "commands": true,
	"ai-providers": true, "image-templates": true, "plugins": true, "settings": true,
	"sync-rules": true, "chat-images": true, "placeholders": true, "player-statistics": true, "skins": true,
}
