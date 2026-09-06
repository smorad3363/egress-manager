//go:build windows

package main

import "golang.org/x/sys/windows"

func isPrivileged() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
