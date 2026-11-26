package desktop

import (
	"bufio"
	"strings"
)

// kconfigValue reads one key of one group from a KDE configuration file
// (kwinrc and the like), and whether it is there. Groups are written
// "[Group]"; a key may carry flags such as "Key[$i]=value".
func kconfigValue(data, group, key string) (string, bool) {
	in := false
	lines := bufio.NewScanner(strings.NewReader(data))
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && !strings.Contains(line, "=") {
			in = line == "["+group+"]"
			continue
		}
		if !in {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if flags := strings.Index(name, "[$"); flags >= 0 {
			name = name[:flags]
		}
		if name == key {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}
