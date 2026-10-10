package monitor

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// parseIostatCPU reads the CPU utilisation out of `LC_ALL=C iostat -c 2 -w 1`: the header's second line names the columns,
// the first data line is the average since boot (not used), the second is the last second. The result is 100 - id,
// clamped to 0–100.
func parseIostatCPU(raw string) (float64, error) {
	var header []string
	var rows [][]string
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if header == nil {
			if indexOf(fields, "us") >= 0 && indexOf(fields, "sy") >= 0 && indexOf(fields, "id") >= 0 {
				header = fields
			}
			continue
		}
		rows = append(rows, fields)
	}
	if header == nil {
		return 0, errors.New("iostat: no us/sy/id header")
	}
	if len(rows) < 2 {
		return 0, fmt.Errorf("iostat: %d data rows, want 2", len(rows))
	}
	row := rows[len(rows)-1]
	idx := indexOf(header, "id")
	if len(row) != len(header) {
		return 0, fmt.Errorf("iostat: row has %d columns, header %d", len(row), len(header))
	}
	idle, err := strconv.ParseFloat(row[idx], 64)
	if err != nil {
		return 0, fmt.Errorf("iostat: id %q: %w", row[idx], err)
	}
	busy := 100 - idle
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, nil
}

func indexOf(fields []string, want string) int {
	for i, f := range fields {
		if f == want {
			return i
		}
	}
	return -1
}
