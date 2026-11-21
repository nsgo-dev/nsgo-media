package ffmpeg

import (
	"syscall"
	"unsafe"
)

var loadLibraryExW = syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW")

// loadWithAlteredSearchPath finds a DLL's own imports beside it.
const loadWithAlteredSearchPath = 0x8

func dlopen(path string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	flags := uintptr(0)
	if containsSeparator(path) {
		flags = loadWithAlteredSearchPath
	}
	h, _, callErr := loadLibraryExW.Call(uintptr(unsafe.Pointer(p)), 0, flags)
	if h == 0 {
		return 0, callErr
	}
	return h, nil
}

func dlsym(h uintptr, name string) (uintptr, error) {
	return syscall.GetProcAddress(syscall.Handle(h), name)
}

func containsSeparator(path string) bool {
	for _, c := range path {
		if c == '\\' || c == '/' {
			return true
		}
	}
	return false
}
