// Package wwwroot embeds the static console UI so the binary is self-contained.
package wwwroot

import "embed"

//go:embed *.html *.js *.css
var FS embed.FS
