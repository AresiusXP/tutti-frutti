//go:build unix

package moderation

import "syscall"

func umaskForTest(mask int) int { return syscall.Umask(mask) }
