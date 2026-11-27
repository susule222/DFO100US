//go:build !windows

package pvf

import (
	"fmt"
	"os"
)

func loadFileBytes(path string, limit int64) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: got %d want <= %d", ErrTooLarge, len(data), limit)
	}
	return data, nil
}
