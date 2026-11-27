// Package channelrefresh implements the current client's separate channel
// directory socket. It never creates accounts or changes the game session keys.
package channelrefresh

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

type Channel struct {
	ID           uint32
	Name         string
	Type         uint32
	Area         string
	SourceValues []int32
}
type Config struct {
	Listen, Advertise, ServerKey, SourceSHA256 string
	ServerID                                   uint32
	MaxUsers                                   uint32
	AreaLabels                                 map[string]string
	Dungeons                                   map[string][]uint32
	Channels                                   []Channel
}

func validIPv4(host string) (net.IP, bool) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, false
	}
	v4 := ip.To4()
	if v4 == nil || len(v4.String()) > 15 {
		return nil, false
	}
	return v4, true
}

func Load(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, e
	}
	h, _, e := net.SplitHostPort(c.Listen)
	if _, ok := validIPv4(h); e != nil || !ok || len(c.SourceSHA256) != 64 || len(c.Channels) == 0 || len(c.Channels) > 128 || len(c.ServerKey) > 19 || c.MaxUsers == 0 {
		return c, fmt.Errorf("invalid channel configuration")
	}
	if c.Advertise != "" {
		if ip, ok := validIPv4(c.Advertise); !ok || ip.IsUnspecified() {
			return c, fmt.Errorf("invalid channel advertise host %q", c.Advertise)
		}
	}
	for _, label := range c.AreaLabels {
		if len(label) == 0 || strings.ContainsAny(label, "`\r\n\x00") {
			return c, fmt.Errorf("invalid area label")
		}
	}
	for _, ch := range c.Channels {
		if ch.ID == 0 || len(ch.Name) == 0 || len(ch.Name) > 18 || strings.ContainsAny(ch.Name, "`\r\n\x00") || len(ch.SourceValues) != 11 {
			return c, fmt.Errorf("invalid source channel row")
		}
	}
	return c, nil
}

func (c Config) Script() []byte {
	var b strings.Builder
	// Local display names are explicit configuration; source area keys/IDs and
	// the eleven channel rule scalars are retained from this client's PVF.
	emitted := make(map[string]bool, len(c.Dungeons))
	for _, ch := range c.Channels {
		if emitted[ch.Area] {
			continue
		}
		ids := c.Dungeons[ch.Area]
		if len(ids) == 0 {
			continue
		} // Source [none] entrance has no dungeon block.
		emitted[ch.Area] = true
		label := c.AreaLabels[ch.Area]
		if label == "" {
			label = strings.Trim(ch.Area, "[]")
		}
		fmt.Fprintf(&b, "[dungeon]\n`%s` `%s`", ch.Area, label)
		for _, id := range ids {
			fmt.Fprintf(&b, " %d", id)
		}
		b.WriteString("\n[/dungeon]\n")
	}
	fmt.Fprintf(&b, "[server]\n%d\n", c.ServerID)
	for _, ch := range c.Channels {
		fmt.Fprintf(&b, "%d `%s` %d `%s`", ch.ID, ch.Name, ch.Type, ch.Area)
		for _, n := range ch.SourceValues {
			fmt.Fprintf(&b, " %d", n)
		}
		b.WriteString(" ``\n")
	}
	b.WriteString("[/server]\n")
	return []byte(b.String())
}

func parseEndpoint(addr, defaultHost string) (string, uint32, error) {
	h, p, e := net.SplitHostPort(addr)
	if e != nil {
		return "", 0, e
	}
	port, e := net.LookupPort("tcp", p)
	if e != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid game endpoint port in %q", addr)
	}
	ip, ok := validIPv4(h)
	if !ok {
		return "", 0, fmt.Errorf("invalid IPv4 game endpoint %q", addr)
	}
	if ip.IsUnspecified() {
		if defaultHost == "" {
			defaultHost = "127.0.0.1"
		}
		defIP, defOK := validIPv4(defaultHost)
		if !defOK || defIP.IsUnspecified() {
			return "", 0, fmt.Errorf("invalid advertise host %q", defaultHost)
		}
		return defIP.String(), uint32(port), nil
	}
	return ip.String(), uint32(port), nil
}

func (c Config) Directory(gameAddress string) ([]byte, error) {
	return c.DirectoryWithEndpoints(nil, gameAddress)
}

func (c Config) DirectoryWithEndpoints(endpoints map[uint32]string, fallback string) ([]byte, error) {
	return c.DirectoryWithEndpointsForHost(endpoints, fallback, c.Advertise)
}

func (c Config) DirectoryWithEndpointsForHost(endpoints map[uint32]string, fallback, advertiseHost string) ([]byte, error) {
	if advertiseHost == "" {
		advertiseHost = c.Advertise
	}
	var fallbackHost string
	var fallbackPort uint32
	if fallback != "" {
		var e error
		fallbackHost, fallbackPort, e = parseEndpoint(fallback, advertiseHost)
		if e != nil {
			return nil, e
		}
	}
	appendFixed := func(b []byte, s string, n int) []byte { out := make([]byte, n); copy(out, s); return append(b, out...) }
	b := binary.LittleEndian.AppendUint32(nil, 1)
	b = appendFixed(b, c.ServerKey, 20)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(c.Channels)))
	for _, ch := range c.Channels {
		h, port := fallbackHost, fallbackPort
		if addr := endpoints[ch.ID]; addr != "" {
			var e error
			h, port, e = parseEndpoint(addr, advertiseHost)
			if e != nil {
				return nil, e
			}
		} else if fallback == "" {
			return nil, fmt.Errorf("missing endpoint for channel %d", ch.ID)
		}
		// Current1451fa5e0 extracts decimal digits from this field to obtain
		// the channel ID; the friendly label lives in the script's channel row.
		b = appendFixed(b, fmt.Sprintf("#%d", ch.ID), 20)
		b = binary.LittleEndian.AppendUint32(b, c.MaxUsers)
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = appendFixed(b, h, 16)
		b = binary.LittleEndian.AppendUint32(b, port)
	}
	return b, nil
}

func frame(id byte, body []byte) []byte {
	b := make([]byte, 11)
	b[0] = 1
	b[1] = id
	b[10] = 1
	binary.LittleEndian.PutUint32(b[2:], uint32(11+len(body)))
	return append(b, body...)
}
func encrypted(data, key []byte) ([]byte, error) {
	block, e := aes.NewCipher(key[:16])
	if e != nil {
		return nil, e
	}
	p := make([]byte, (len(data)+15)/16*16)
	copy(p, data)
	for i := 0; i < len(p); i += 16 {
		block.Encrypt(p[i:], p[i:])
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	if _, e = w.Write(p); e != nil {
		return nil, e
	}
	if e = w.Close(); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}

func Serve(c Config, gameAddress string, event func(map[string]any)) (net.Listener, error) {
	return ServeWithEndpoints(c, nil, gameAddress, event)
}

func ServeWithEndpoints(c Config, endpoints map[uint32]string, fallback string, event func(map[string]any)) (net.Listener, error) {
	defaultDir, e := c.DirectoryWithEndpoints(endpoints, fallback)
	if e != nil {
		return nil, e
	}
	l, e := net.Listen("tcp4", c.Listen)
	if e != nil {
		return nil, e
	}
	script := c.Script()
	go func() {
		for {
			conn, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				start := time.Now()
				dir := defaultDir
				// When game endpoints are bound to 0.0.0.0 and no explicit Advertise IP
				// is pinned, advertise the interface IP this client actually connected to
				// (e.g. 127.0.0.1 for local play, 192.168.x.x for LAN peers).
				if c.Advertise == "" {
					if ta, ok := conn.LocalAddr().(*net.TCPAddr); ok {
						if v4 := ta.IP.To4(); v4 != nil && !v4.IsUnspecified() {
							if custom, err := c.DirectoryWithEndpointsForHost(endpoints, fallback, v4.String()); err == nil {
								dir = custom
							}
						}
					}
				}
				e := handle(conn, script, dir)
				event(map[string]any{"kind": "channel_refresh_finished", "peer": conn.RemoteAddr().String(), "success": e == nil, "elapsed_ms": time.Since(start).Milliseconds(), "error": errorText(e)})
			}()
		}
	}()
	return l, nil
}
func errorText(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func handle(conn net.Conn, script, directory []byte) error {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var seed [16]byte
	if _, e := rand.Read(seed[:]); e != nil {
		return e
	}
	key := []byte(hex.EncodeToString(seed[:]))
	authed := false
	for i := 0; i < 5; i++ {
		h := make([]byte, 11)
		if _, e := io.ReadFull(conn, h); e != nil {
			return e
		}
		n := binary.LittleEndian.Uint32(h[2:])
		if h[0] != 0 || h[10] != 1 || n < 11 || n > 64 {
			return fmt.Errorf("invalid channel request header")
		}
		body := make([]byte, int(n)-11)
		if _, e := io.ReadFull(conn, body); e != nil {
			return e
		}
		var reply []byte
		switch h[1] {
		case 11:
			if authed || len(body) != 32 {
				return fmt.Errorf("invalid channel preface")
			}
			authed = true
			reply = frame(12, append(make([]byte, 4), key...))
		case 9:
			if !authed || len(body) != 0 {
				return fmt.Errorf("invalid channel script request")
			}
			// Client first consumes the key response, then the script response.
			p, e := encrypted(script, key)
			if e != nil {
				return e
			}
			reply = frame(10, p)
		case 1:
			if !authed || len(body) != 0 {
				return fmt.Errorf("invalid directory request")
			}
			p, e := encrypted(directory, key)
			if e != nil {
				return e
			}
			reply = frame(3, p)
		default:
			return fmt.Errorf("unsupported channel request %d", h[1])
		}
		if _, e := io.Copy(conn, bytes.NewReader(reply)); e != nil {
			return e
		}
		if h[1] == 1 {
			return nil
		}
	}
	return fmt.Errorf("channel handshake exceeded bound")
}
