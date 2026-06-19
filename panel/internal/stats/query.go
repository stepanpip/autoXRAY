package stats

import (
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
)

type statEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type statResp struct {
	Stat []statEntry `json:"stat"`
}

// parseStatsJSON groups "user>>>NAME>>>traffic>>>uplink|downlink" counters by user.
func parseStatsJSON(b []byte) (map[string]Snapshot, error) {
	var r statResp
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	out := map[string]Snapshot{}
	for _, e := range r.Stat {
		parts := strings.Split(e.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		name, dir := parts[1], parts[3]
		v, _ := strconv.ParseInt(e.Value, 10, 64)
		s := out[name]
		switch dir {
		case "uplink":
			s.Up += v
		case "downlink":
			s.Down += v
		}
		out[name] = s
	}
	return out, nil
}

// Query runs xray statsquery with reset and returns per-user deltas.
func Query(server string) (map[string]Snapshot, error) {
	cmd := exec.Command("xray", "api", "statsquery",
		"--server="+server, "-pattern", "user>>>", "-reset")
	b, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseStatsJSON(b)
}
