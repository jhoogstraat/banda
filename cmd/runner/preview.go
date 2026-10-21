package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/tunnel"
)

// dialPort connects a tunnel the API opened to a port in the pod. Only
// listening ports above 1023 are reachable, like reportPorts lists them.
func dialPort(ctx context.Context, c *tunnel.Conn, port uint32) {
	if port < 1024 || port > 65535 {
		c.CloseWithError(fmt.Errorf("port %d is not previewable", port))
		return
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	tcp, err := d.DialContext(ctx, "tcp", net.JoinHostPort("localhost", strconv.Itoa(int(port))))
	if err != nil {
		c.CloseWithError(fmt.Errorf("nothing answers on port %d", port))
		return
	}
	go func() {
		io.Copy(tcp, c)
		tcp.Close()
	}()
	_, err = io.Copy(c, tcp)
	if err != nil {
		c.CloseWithError(err)
	} else {
		c.Close()
	}
	tcp.Close()
}

// reportPorts sends the listening ports at start and whenever they change.
// ponytail: polls /proc every 2 s; inotify can't watch it, netlink sock_diag could.
func reportPorts(ctx context.Context, send func(*bandav1.ConnectRequest) error) {
	var last []*bandav1.PreviewPort
	for first := true; ; first = false {
		ports := listening()
		if first || !slices.EqualFunc(ports, last, func(a, b *bandav1.PreviewPort) bool { return a.Port == b.Port && a.Process == b.Process }) {
			if send(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_PreviewPorts{PreviewPorts: &bandav1.PreviewPorts{Ports: ports}}}) != nil {
				return
			}
			last = ports
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// listening returns the TCP ports above 1023 that something in the pod
// listens on, sorted, with the process that owns each socket.
func listening() []*bandav1.PreviewPort {
	inodes := map[string]uint32{} // socket inode -> port
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, _ := os.ReadFile(f)
		for port, inode := range parseListen(string(b)) {
			inodes[inode] = port
		}
	}
	names := processes(inodes)
	byPort := map[uint32]string{} // a port can listen on IPv4 and IPv6
	for inode, port := range inodes {
		byPort[port] = cmp.Or(byPort[port], names[inode])
	}
	var out []*bandav1.PreviewPort
	for port, name := range byPort {
		out = append(out, &bandav1.PreviewPort{Port: port, Process: name})
	}
	slices.SortFunc(out, func(a, b *bandav1.PreviewPort) int { return int(a.Port) - int(b.Port) })
	return out
}

// parseListen reads /proc/net/tcp{,6}: the listening sockets (state 0A) with
// a port above 1023, as port by socket inode.
func parseListen(table string) map[uint32]string {
	out := map[uint32]string{}
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		_, hex, _ := strings.Cut(f[1], ":")
		port, err := strconv.ParseUint(hex, 16, 16)
		if err == nil && port > 1023 {
			out[uint32(port)] = f[9]
		}
	}
	return out
}

// processes names the process holding each socket inode, where it can see it
// (the same user's processes).
func processes(inodes map[string]uint32) map[string]string {
	out := map[string]string{}
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range fds {
		link, err := os.Readlink(fd)
		inode, ok := strings.CutPrefix(link, "socket:[")
		if err != nil || !ok {
			continue
		}
		inode = strings.TrimSuffix(inode, "]")
		if _, ok := inodes[inode]; !ok || out[inode] != "" {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join(fd, "../../cmdline"))
		out[inode] = processName(strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00"))
	}
	return out
}

// processName is the telling part of a command line: the script an
// interpreter runs ("vite", "next", "http.server"), else the program.
func processName(args []string) string {
	base := func(s string) string { return strings.TrimSuffix(filepath.Base(s), filepath.Ext(s)) }
	if len(args) == 0 || args[0] == "" {
		return ""
	}
	prog := filepath.Base(args[0])
	switch strings.TrimRight(prog, "0123456789.") {
	case "node", "bun", "deno", "python", "ruby", "php", "java":
		for i := 1; i < len(args); i++ {
			switch a := args[i]; {
			case a == "-m" && i+1 < len(args):
				return args[i+1]
			case a == "run" || strings.HasPrefix(a, "-"): // deno run, bun run, flags
			default:
				return base(a)
			}
		}
	}
	return prog
}
