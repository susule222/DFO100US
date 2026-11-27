package main

import (
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// injectSender holds the current session's packet sender so a watched file can
// push candidate payloads during layout recovery without a client restart.
var injectSender atomic.Value

// watchInject polls path and, whenever its bytes change, sends them on the
// active session. The command id comes from the file name (inject-<id>.bin, so
// the layout-recovery harness can replay any packet, not just id 2). Without
// this, every payload experiment costs a client restart plus a character
// selection, which is exactly the loop that made the earlier rounds slow.
//
// Recovery only: playable runs leave -inject-watch empty.
func watchInject(path string) {
	id := uint16(2)
	if base := filepath.Base(path); strings.HasPrefix(base, "inject-") {
		if dash := strings.Index(base, "-"); dash >= 0 {
			if dot := strings.Index(base[dash+1:], "."); dot > 0 {
				if n, e := strconv.Atoi(base[dash+1 : dash+1+dot]); e == nil && n > 0 && n < 65535 {
					id = uint16(n)
				}
			}
		}
	}
	log.Printf("inject-watch: watching %s as id %d", path, id)
	last := ""
	for {
		if raw, e := os.ReadFile(path); e == nil && len(raw) > 0 {
			key := fmt.Sprintf("%d:%08x", len(raw), crc32.ChecksumIEEE(raw))
			if key != last {
				last = key
				if send, ok := injectSender.Load().(func(byte, uint16, []byte) error); ok && send != nil {
					if e := send(0, id, raw); e != nil {
						log.Printf("inject-watch: send failed: %v", e)
					} else {
						log.Printf("inject-watch: sent %d bytes as id %d", len(raw), id)
					}
				} else {
					log.Printf("inject-watch: no active session yet")
				}
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
}

// watchInjectDir polls a directory for "inject-<id>.bin" files and sends each
// file's bytes as that command id whenever they change.
//
// The single-file watcher above is pinned to one id (id 2 for userinfo work),
// which is enough for payload experiments on a known packet but not for asking
// "does this build accept this notification at all" — the question that matters
// when the value to place has no known channel yet. Naming the id in the file
// keeps the same property as the single-file form: a payload experiment costs a
// file write, not a client restart.
func watchInjectDir(dir string) {
	sent := map[string]string{}
	log.Printf("inject-watch-dir: watching %s for inject-<id>.bin", dir)
	for {
		entries, err := os.ReadDir(dir)
		if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(name, "inject-") || !strings.HasSuffix(name, ".bin") {
				continue
			}
			// inject-<id>.bin sends as kind 0; inject-<kind>-<id>.bin sends as
			// that kind. Kind 1 (CMD) reaches a different dispatch table than
			// kind 0 (NOTI), and several handlers in this build's CORE_CMD table
			// (for example the local HP sync) are only reachable there.
			id := uint16(2)
			kind := byte(0)
			body := strings.TrimSuffix(strings.TrimPrefix(name, "inject-"), ".bin")
			if parts := strings.Split(body, "-"); len(parts) == 2 {
				if k, e := strconv.Atoi(parts[0]); e == nil && k >= 0 && k <= 1 {
					kind = byte(k)
				}
				body = parts[1]
			}
			if n, e := strconv.Atoi(body); e == nil && n > 0 && n < 65535 {
				id = uint16(n)
			} else {
				continue
			}
			raw, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil || len(raw) == 0 {
				continue
			}
			key := fmt.Sprintf("%d:%08x", len(raw), crc32.ChecksumIEEE(raw))
			if sent[name] == key {
				continue
			}
			sent[name] = key
			if send, ok := injectSender.Load().(func(byte, uint16, []byte) error); ok && send != nil {
				if e := send(kind, id, raw); e != nil {
					log.Printf("inject-watch-dir: %s send failed: %v", name, e)
				} else {
					log.Printf("inject-watch-dir: sent %s (%d bytes) as kind %d id %d", name, len(raw), kind, id)
				}
			}
		}
		}
		time.Sleep(500 * time.Millisecond)
	}
}
