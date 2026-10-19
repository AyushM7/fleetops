package visitor

import (
	"syscall"
	"unsafe"
)

func availableBytes(path string) (uint64, error) {
	pointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var free uint64
	result, _, callErr := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW").Call(uintptr(unsafe.Pointer(pointer)), uintptr(unsafe.Pointer(&free)), 0, 0)
	if result == 0 {
		return 0, callErr
	}
	return free, nil
}
