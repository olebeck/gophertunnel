//go:build !windows

package resource

import "os"

func createTemp(name string) (*os.File, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	err = os.Remove(name)
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
