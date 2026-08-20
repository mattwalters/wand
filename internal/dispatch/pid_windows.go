//go:build windows

package dispatch

import "golang.org/x/sys/windows"

// CI runs Linux and macOS, so this file is compile-checked there and
// exercised only on a Windows machine.

// processAlive opens the process with the minimal query right and reads its
// exit code: STILL_ACTIVE means running, anything else (including a failed
// open, which usually means the pid no longer exists) means gone.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}
