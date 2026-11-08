// SPDX-License-Identifier: AGPL-3.0-only
// Package api embeds the draft contracts so the binary does not depend on cwd.
package api

import _ "embed"

//go:embed openapi.json
var OpenAPI []byte

//go:embed protocol/endpoint.schema.json
var EndpointSchema []byte
