//go:build linux

package desktop

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AppOfConnection finds the app whose process holds the other end of a
// loopback connection to this process: the socket is looked up in
// /proc/net, its owner among this user's processes, and the app is the one
// that showed a window from that process or one above it.
func AppOfConnection(remote string) (string, error) {
	k, _ := desktopKWin()
	if k == nil {
		return "", errors.New("finding the app of a connection needs KDE Plasma")
	}
	host, port, err := net.SplitHostPort(remote)
	if err != nil {
		return "", err
	}
	inode, err := socketInode(net.ParseIP(host), port)
	if err != nil {
		return "", err
	}
	pid, err := processOfSocket(inode)
	if err != nil {
		return "", err
	}
	id, ok := k.appOfProcess(pid)
	if !ok {
		return "", fmt.Errorf("process %d belongs to no app that showed a window", pid)
	}
	return id, nil
}

// socketInode finds the inode of the TCP socket bound to ip:port.
func socketInode(ip net.IP, port string) (string, error) {
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", err
	}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(table)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) > 9 && matchesAddress(fields[1], ip, uint16(number)) {
				file.Close()
				return fields[9], nil
			}
		}
		file.Close()
	}
	return "", fmt.Errorf("no socket at %s:%s", ip, port)
}

// matchesAddress compares an address from /proc/net/tcp or tcp6 with ip and
// port. IPv6 tables hold four little-endian words; an IPv4 client of an
// IPv6 socket appears there as ::ffff:a.b.c.d.
func matchesAddress(field string, ip net.IP, port uint16) bool {
	address, portHex, found := strings.Cut(field, ":")
	if !found {
		return false
	}
	if got, err := strconv.ParseUint(portHex, 16, 16); err != nil || uint16(got) != port {
		return false
	}
	raw, err := hex.DecodeString(address)
	if err != nil {
		return false
	}
	for i := 0; i+4 <= len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return net.IP(raw).Equal(ip)
}

// processOfSocket finds which of this user's processes holds the socket.
func processOfSocket(inode string) (int, error) {
	target := "socket:[" + inode + "]"
	processes, _ := filepath.Glob("/proc/[0-9]*")
	for _, dir := range processes {
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name())); err == nil && link == target {
				return strconv.Atoi(filepath.Base(dir))
			}
		}
	}
	return 0, fmt.Errorf("no process of this user holds socket %s", inode)
}
