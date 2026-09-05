module github.com/nyxveil/controlplane-core-interop

go 1.24

require github.com/nyxveil/nvp v0.0.0

require github.com/golang-jwt/jwt/v5 v5.2.1 // indirect

// Neutral placeholder. scripts/test-core-interop.ps1 rewrites this line to the
// extracted Frozen Core path for the duration of the harness run only.
replace github.com/nyxveil/nvp => ../.frozen-core-placeholder
