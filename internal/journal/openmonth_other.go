//go:build !unix

package journal

// openMonthFile opens a month file for reading. Without O_NOFOLLOW a symlink is refused by the Lstat check of
// openLstatChecked, which also refuses a file swapped in after that check; the descriptor is then checked again by ReadUnit.
func openMonthFile(path string) (monthFile, error) {
	f, err := openLstatChecked(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}
