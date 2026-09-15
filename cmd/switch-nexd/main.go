// Command switch-nexd serves every Switch-era NEX title on the switch-nex
// lineage: one process, one TLS listener per endpoint (auth on the title's
// port base, secure on base+1), generic LoginEx + TestConnectivity handlers.
//
// Per-title protocol work (matchmaking shapes, DataStore, ranking) is
// registered per title as it is implemented; unimplemented methods answer
// with an RMC error and are logged — the game tells us the backlog.
package main

import (
	"crypto/sha512"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

type title struct {
	name string
	base int
}

var titles = []title{
	{"mario-party-superstars", 22110},
	{"monster-hunter-generations-ultimate", 22120},
	{"mario-golf-super-rush", 22130},
	{"arms", 22140},
	{"mario-kart-8-deluxe", 22150},
	{"super-mario-bros-35", 22160},
	{"mario-tennis-aces", 22170},
	{"animal-crossing-new-horizons", 22180},
	{"luigis-mansion-3", 22190},
	{"mario-strikers-battle-league", 22300},
}

var (
	stationAddress = flag.String("station-address", "145.241.199.19", "address carried in every station URL (raw IP; the console dials it without DNS)")
	certFile       = flag.String("cert", "/app/nex.pem", "TLS certificate (CN covers *.s.n.srv.nintendo.net)")
	keyFile        = flag.String("key", "/app/nex.key", "TLS key")
	only           = flag.String("only", "", "comma-separated title names to serve (default: all)")
)

// recall remembers login PID by client IP for the ticketless secure stage.
type recall struct {
	mu  sync.Mutex
	m   map[string]recallEntry
	ttl time.Duration
}

type recallEntry struct {
	pid uint64
	at  time.Time
}

func newRecall(ttl time.Duration) *recall {
	return &recall{m: map[string]recallEntry{}, ttl: ttl}
}

func (r *recall) remember(host string, pid uint64) {
	r.mu.Lock()
	r.m[host] = recallEntry{pid: pid, at: time.Now()}
	r.mu.Unlock()
}

func (r *recall) take(host string) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[host]
	if !ok || time.Since(e.at) > r.ttl {
		return 0, false
	}
	return e.pid, true
}

// pidFor returns a stable NEX PID for a login username: the console must keep
// recognizing itself as the same principal across logins.
func pidFor(username string) uint64 {
	h := sha512.Sum512_256([]byte("openpak-switch-nex-pid:" + username))
	return 0x6C000000 | uint64(binary.BigEndian.Uint32(h[:4])&0x00FFFFFF)
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("cert: %v", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	want := map[string]bool{}
	if *only != "" {
		for _, n := range splitCSV(*only) {
			want[n] = true
		}
	}

	recall := newRecall(24 * time.Hour)

	for _, t := range titles {
		if len(want) > 0 && !want[t.name] {
			continue
		}
		auth := &prudplite.Endpoint{
			StreamID: 1,
			OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
				return handleAuthRMC(recall, *stationAddress, t, c, msg)
			},
		}
		secure := &prudplite.Endpoint{
			StreamID: 1,
			IsSecure: true,
			RecallPID: func(host string) (uint64, bool) {
				pid, ok := recall.take(host)
				if ok {
					log.Printf("[%s] secure: recalled pid=%d for %s", t.name, pid, host)
				}
				return pid, ok
			},
			OnConnect: func(c *prudplite.Connection) {
				log.Printf("[%s] secure: connected pid=%d host=%s", t.name, c.PID, c.Host)
			},
			OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
				if msg.Protocol == 0x0B && msg.MethodID == 0x05 {
					return rmc.EncodeSuccess(0x0B, msg.CallID, nil) // TestConnectivity
				}
				log.Printf("[%s] secure unimplemented proto=0x%X method=0x%X (%dB)", t.name, msg.Protocol, msg.MethodID, len(msg.Body))
				return rmc.EncodeError(msg.Protocol, msg.CallID, 0x00010001)
			},
		}

		authLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] auth listen :%d: %v", t.name, t.base, err)
			continue
		}
		secureLn, err := tls.Listen("tcp", fmt.Sprintf(":%d", t.base+1), tlsCfg.Clone())
		if err != nil {
			log.Printf("[%s] secure listen :%d: %v", t.name, t.base+1, err)
			continue
		}
		log.Printf("[%s] serving auth :%d secure :%d", t.name, t.base, t.base+1)
		go auth.Listen(authLn)
		go secure.Listen(secureLn)
	}

	select {} // serve forever
}

// handleAuthRMC answers the auth-connection RMCs. Only LoginEx is expected
// here; anything else logs and answers unimplemented.
func handleAuthRMC(recall *recall, stationAddress string, t title, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.Protocol == 0x0A && msg.MethodID == 0x02 {
		return handleLoginEx(recall, stationAddress, t, c, msg)
	}
	log.Printf("[%s] auth unimplemented proto=0x%X method=0x%X (%dB)", t.name, msg.Protocol, msg.MethodID, len(msg.Body))
	return rmc.EncodeError(msg.Protocol, msg.CallID, 0x00010001)
}

// handleLoginEx mints the Switch v1 ticket and returns the verified
// ValidateAndRequestTicketResult body.
func handleLoginEx(recall *recall, stationAddress string, t title, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	username, _ := readString(msg.Body, 0)
	pid := pidFor(username)

	sourceKey := make([]byte, switchnex.RandomKeyLen)
	if _, err := randomRead(sourceKey); err != nil {
		log.Printf("[%s] login: rand: %v", t.name, err)
		return rmc.EncodeError(0x0A, msg.CallID, 0x00010001)
	}
	now := uint64(time.Now().UnixNano())
	ticket, sessionKey, err := switchnex.Mint(sourceKey, pid, now)
	if err != nil {
		log.Printf("[%s] login: mint: %v", t.name, err)
		return rmc.EncodeError(0x0A, msg.CallID, 0x00010001)
	}
	_ = sessionKey

	url := switchnex.New(stationAddress, uint16(t.base+1))
	body := switchnexResult(t, url.String(), ticket, now, pid, sourceKey)

	recall.remember(c.Host, pid)
	log.Printf("[%s] login pid=%d url=%q ticket=%dB", t.name, pid, url.String(), len(ticket))
	return rmc.EncodeSuccess(0x0A, msg.CallID, body)
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s + "," {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func randomRead(b []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.Read(b)
}
