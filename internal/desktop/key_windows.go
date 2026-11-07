//go:build windows

package desktop

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

func transformKey(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty protected key")
	}
	input := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var output windows.DataBlob
	var err error
	if decrypt {
		err = windows.CryptUnprotectData(&input, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	} else {
		description, _ := windows.UTF16PtrFromString("PikPak Personal data encryption key")
		err = windows.CryptProtectData(&input, description, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output)
	}
	runtime.KeepAlive(data)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	return append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...), nil
}
func protectKey(data []byte) ([]byte, error)   { return transformKey(data, false) }
func unprotectKey(data []byte) ([]byte, error) { return transformKey(data, true) }
