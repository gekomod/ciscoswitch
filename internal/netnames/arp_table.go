package netnames

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// HostARP czyta tablicę sąsiadów (ARP) komputera, na którym działa panel.
// Na Linuksie /proc/net/arp, na Windows i macOS polecenie arp.
func HostARP() map[string][]string {
	if runtime.GOOS == "linux" {
		if b, err := os.ReadFile("/proc/net/arp"); err == nil {
			return ParseARPTable(string(b))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := []string{"-a"}
	if runtime.GOOS != "windows" {
		args = []string{"-an"}
	}
	out, err := exec.CommandContext(ctx, "arp", args...).Output()
	if err != nil {
		return map[string][]string{}
	}
	return ParseARPTable(string(out))
}
