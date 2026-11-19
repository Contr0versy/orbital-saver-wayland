//go:build !amd64 && !arm64

package wayland

// Unknown here; anonymousFile falls back to an unlinked file.
const sysMemfdCreate = 0
