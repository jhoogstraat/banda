package main

import (
	"maps"
	"testing"
)

func TestParseListen(t *testing.T) {
	table := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 222 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F40 0100007F:9C40 01 00000000:00000000 00:00000000 00000000  1000        0 333 1 0000000000000000 20 4 30 10 -1
`
	got := parseListen(table)
	if !maps.Equal(got, map[uint32]string{5173: "111"}) {
		t.Fatalf("got %v: want only the listener on 5173 (port 80 is a system port, 8000 is connected)", got)
	}
}

func TestProcessName(t *testing.T) {
	for want, args := range map[string][]string{
		"vite":        {"node", "/w/node_modules/.bin/vite", "--port", "5173"},
		"next":        {"/usr/bin/node", "/w/node_modules/next/dist/bin/next.js", "dev"},
		"http.server": {"python3", "-m", "http.server", "8000"},
		"main":        {"deno", "run", "-A", "main.ts"},
		"server":      {"./server"},
		"":            {""},
	} {
		if got := processName(args); got != want {
			t.Errorf("processName(%q) = %q, want %q", args, got, want)
		}
	}
}
