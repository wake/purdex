//go:build !unix

package journal

import "os"

// openMonthFile opens a month file for reading; the check on the descriptor refuses what is not a regular file.
func openMonthFile(path string) (monthFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}
