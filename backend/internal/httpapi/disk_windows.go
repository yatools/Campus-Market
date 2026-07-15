//go:build windows

package httpapi

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func diskFreeBytes(path string) (uint64, error) {
	root := filepath.VolumeName(path) + `\`
	pointer, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var available uint64
	err = windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil)
	_ = unsafe.Pointer(pointer)
	return available, err
}
