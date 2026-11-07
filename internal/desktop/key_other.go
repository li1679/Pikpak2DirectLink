//go:build !windows

package desktop

import "errors"

func protectKey([]byte) ([]byte, error) {
	return nil, errors.New("automatic desktop key protection requires Windows")
}
func unprotectKey([]byte) ([]byte, error) {
	return nil, errors.New("automatic desktop key protection requires Windows")
}
