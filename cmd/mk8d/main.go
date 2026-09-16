// Command mk8d — the Mario Kart 8 Deluxe server on the switch-nex lineage.
//
// Auth endpoint (22150/TLS, reached via the edge on 443 by SNI): PRUDPLite
// CONNECT, then TicketGranting 0x0A method 0x02 (LoginEx). The response is
// the flat result body (result.go) with a freshly minted two-layer Switch
// v1 ticket (kerberos.go).
//
// Secure endpoint (22151/TLS, dialed from the station URL): the client's
// CONNECT carries the kerberos ticket and check-data; the ticket decrypts
// with the secure account key here. Empty CONNECTs fall back to the login
// PID recall by client IP (SPEC.md). SecureConnection 0x0B method 0x05
// (TestConnectivity) is answered; everything else is logged.
package main

import (
	"crypto/rand"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

var (
	authPort       = flag.Int("auth-port", 22150, "auth listener (reached via the edge on 443, SNI-routed)")
	securePort     = flag.Int("secure-port", 22151, "secure listener (dialed directly from the station URL)")
	stationAddress = flag.String("station-address", "145.241.199.19", "address carried in the station URL (raw IP)")
	accountPID     = flag.Uint64("account-pid", 1900000001, "the account's NEX PID (what the game knows itself by)")
	accessKey      = flag.String("access-key", "09c1c475", "MK8D's PRUDP access key (verifies the CONNECT signature)")
	securePID      = flag.Uint64("secure-pid", 2, "the secure server's NEX PID")
	securePassword = flag.String("secure-password", "openpak-mk8d-secure", "the secure server's kerberos password")
	certFile       = flag.String("cert", "", "TLS certificate for both listeners")
	keyFile        = flag.String("key", "", "TLS key for both listeners")
)

// recall remembers login PID by client IP for the ticketless secure stage
// (TTL-bounded; a stale entry must not hand a session to a different console).
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

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)

	if *certFile == "" || *keyFile == "" {
		log.Fatal("-cert and -key are required (both listeners are TLS)")
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("cert: %v", err)
	}

	// The secure account's kerberos key: the auth endpoint encrypts the
	// ticket's inner layer with it, the secure endpoint decrypts it.
	targetKey := switchnex.DeriveKey([]byte(*securePassword), *securePID)
	recall := newRecall(24 * time.Hour)

	// --- Auth endpoint: TicketGranting (0x0A) ---
	auth := &prudplite.Endpoint{
		StreamID:  1,
		AccessKey: *accessKey,
		OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
			if msg.Protocol == 0x0A && msg.MethodID == 0x02 {
				return handleLoginEx(c, msg, recall, targetKey)
			}
			log.Printf("auth: unimplemented proto=0x%X method=0x%X", msg.Protocol, msg.MethodID)
			return rmc.EncodeError(msg.Protocol, msg.CallID, 0x00010001)
		},
	}

	// --- Secure endpoint: kerberos ticket CONNECT, PID recall fallback ---
	secure := &prudplite.Endpoint{
		StreamID:    1,
		IsSecure:    true,
		AccessKey:   *accessKey,
		KerberosKey: targetKey,
		RecallPID: func(host string) (uint64, bool) {
			pid, ok := recall.take(host)
			if ok {
				log.Printf("secure: recalled pid=%d for %s", pid, host)
			}
			return pid, ok
		},
		OnConnect: func(c *prudplite.Connection) {
			log.Printf("secure: connected pid=%d cid=%d host=%s", c.PID, c.CID, c.Host)
		},
		OnRMC: func(c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
			// SecureConnection 0x0B method 0x05: TestConnectivity — reply with
			// an empty success (the console is asking whether the socket still
			// carries RMC).
			if msg.Protocol == 0x0B && msg.MethodID == 0x05 {
				return rmc.EncodeSuccess(0x0B, msg.CallID, msg.MethodID, nil)
			}
			log.Printf("secure: unimplemented proto=0x%X method=0x%X (%d bytes)", msg.Protocol, msg.MethodID, len(msg.Body))
			return rmc.EncodeError(msg.Protocol, msg.CallID, 0x00010001)
		},
	}

	// MK8D's NEX client negotiates legacy TLS (Switch nn::ssl); the Go default
	// minimum of 1.2 rejects its ClientHello outright.
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
	}

	authLn, err := tls.Listen("tcp", net.JoinHostPort("", itoa(*authPort)), tlsCfg.Clone())
	if err != nil {
		log.Fatalf("auth listen: %v", err)
	}
	secureLn, err := tls.Listen("tcp", net.JoinHostPort("", itoa(*securePort)), tlsCfg.Clone())
	if err != nil {
		log.Fatalf("secure listen: %v", err)
	}

	log.Printf("mk8d (switch-nex): auth :%d secure :%d station=%s:%d pid=%d secure-pid=%d accesskey=%s",
		*authPort, *securePort, *stationAddress, *securePort, *accountPID, *securePID, *accessKey)
	auth.Listen(authLn)
	secure.Listen(secureLn)
	select {}
}

// handleLoginEx processes TicketGranting 0x0A method 0x02. The request body
// carries the account username string and a DataHolder with the BAAS token;
// identity is already established at the account layer, so the handler mints
// the two-layer ticket, registers the IP recall, and returns the flat result
// body.
func handleLoginEx(c *prudplite.Connection, msg *rmc.RMCMessage, recall *recall, targetKey []byte) []byte {
	// The account layer has already proven the identity; the PID is the
	// account's configured NEX PID.
	pid := *accountPID

	sourceKey := make([]byte, switchnex.RandomKeyLen)
	if _, err := rand.Read(sourceKey); err != nil {
		log.Printf("login: rand: %v", err)
		return rmc.EncodeError(0x0A, msg.CallID, 0x00010001)
	}
	ticket, _, err := switchnex.Mint(sourceKey, pid, *securePID, targetKey)
	if err != nil {
		log.Printf("login: mint: %v", err)
		return rmc.EncodeError(0x0A, msg.CallID, 0x00010001)
	}

	url := switchnex.New(*stationAddress, uint16(*securePort))
	url.SetInt("PID", *securePID)
	body := switchnex.Result{
		RetVal:         rmc.ResultSuccess,
		SourcePID:      pid,
		Ticket:         ticket,
		StationURL:     url.String(),
		SpecialStation: "prudp",
		CurrentUTCTime: switchnex.NowDateTime(),
		ReturnMsg:      "OpenPak mario-kart-8-deluxe",
		SourceKeyHex:   switchnex.SourceKeyHex(sourceKey),
	}.Bytes()

	recall.remember(c.Host, pid)
	log.Printf("LoginEx: pid=%d url=%q ticket=%dB", pid, url.String(), len(ticket))
	return rmc.EncodeSuccess(0x0A, msg.CallID, msg.MethodID, body)
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}
