//go:build windows

package resource

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func createTemp(name string) (*os.File, error) {
	const FILE_FLAG_DELETE_ON_CLOSE = 0x04000000
	path, _ := syscall.UTF16PtrFromString(name)
	hand, err := windows.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_DELETE, nil, syscall.TRUNCATE_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL|FILE_FLAG_DELETE_ON_CLOSE, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(hand), name), nil
}
