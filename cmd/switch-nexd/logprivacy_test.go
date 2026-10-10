package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/openpak/switch-nex/rmc"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A login and a secure session must leave no pid, account id, nickname, token
// or login name in the log: tags only.
func TestLogsCarryNoPersonalData(t *testing.T) {
	const accountPID = 0x1122334455
	var out lockedBuf
	log.SetOutput(&out)
	defer log.SetOutput(log.Writer())

	baas := newFakeBaas(t, accountPID)
	ids := newIdentity(baas.URL, "secret-key")
	authAddr, secureAddr, _ := startWith(t, "mario-kart-8-deluxe", ids)
	key := titleAccessKey(t, "mario-kart-8-deluxe")
	token := jwt(t, map[string]any{"sub": "user-1", "openpak": "nx2.bound"})

	c := dial(t, authAddr, key)
	c.handshake(nil)
	w := rmc.NewWriter()
	w.String("ignored-name")
	w.Raw(authInfoHolder(token))
	resp := c.call(0x0A, 0x02, w.Bytes())
	if resp.IsError {
		t.Fatalf("login refused: %#08x", resp.ErrorCode|rmc.ErrorCodeBit)
	}
	c.readLoginExResult(resp.Body)
	c.openTicket()

	s := dial(t, secureAddr, key)
	s.pid, s.ticket, s.sessionKey = c.pid, c.ticket, c.sessionKey
	s.handshake(s.secureConnectPayload(1))
	s.call(ProtoUtility, 0x01, nil)

	got := out.String()
	if !strings.Contains(got, "login pid=~") {
		t.Fatalf("login line missing or untagged:\n%s", got)
	}
	for _, secret := range []string{
		"Zorblax", "nsa-abc", "acct-1", "ignored-name", token, "user-1",
		fmt.Sprint(uint64(accountPID)), fmt.Sprintf("%x", accountPID),
	} {
		if strings.Contains(got, secret) {
			t.Errorf("log contains %q:\n%s", secret, got)
		}
	}
}
