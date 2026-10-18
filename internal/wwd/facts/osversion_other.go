//go:build !darwin

package facts

import (
	"bufio"
	"os"
	"strings"
)

// osVersion is VERSION_ID of /etc/os-release ("26.04" on Ubuntu 26.04).
func osVersion() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VERSION_ID="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}
