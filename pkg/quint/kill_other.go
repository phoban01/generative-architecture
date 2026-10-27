//go:build !unix

package quint

import "os/exec"

// killGroup keeps the default cancellation, which kills Quint only.
func killGroup(*exec.Cmd) {}
