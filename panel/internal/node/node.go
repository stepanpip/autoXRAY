package node

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func parseLoadAvg(s string) (float64, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty loadavg")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// Load returns the 1-minute load average (Linux /proc/loadavg).
func Load() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	return parseLoadAvg(string(b))
}
