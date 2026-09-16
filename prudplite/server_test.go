package prudplite

import (
	"bytes"
	"encoding/binary"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

var (
	testTargetKey = switchnex.DeriveKey([]byte("securepassword"), 2)
	testSourceKey = bytes.Repeat([]byte{0x11}, 32)
)

// A secure CONNECT payload is [buffer ticket][buffer check-data]; the reply
// proves the session key: [u32 4][u32 check+1].
func TestProcessConnectPayload(t *testing.T) {
	c := &Connection{Endpoint: &Endpoint{IsSecure: true, KerberosKey: testTargetKey}}

	ticket, sessionKey, err := switchnex.Mint(testSourceKey, 1900000001, 2, testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	// The CONNECT carries the INNER ticket: the client unwraps the outer
	// layer with its source key and hands us the internal blob.
	clientTicket, err := switchnex.DecryptClientTicket(ticket, testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	innerTicket := clientTicket.Internal

	// Check data: kerberos-encrypted (PID, CID, check) under the session key.
	check := bytes.Repeat([]byte{0}, 16)
	binary.LittleEndian.PutUint64(check[0:8], 1900000001)
	binary.LittleEndian.PutUint32(check[8:12], 5)    // CID
	binary.LittleEndian.PutUint32(check[12:16], 400) // check
	encryptedCheck := switchnex.EncryptSum(sessionKey, check)

	payload := binary.LittleEndian.AppendUint32(nil, uint32(len(innerTicket)))
	payload = append(payload, innerTicket...)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len(encryptedCheck)))
	payload = append(payload, encryptedCheck...)

	response, err := c.processConnectPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if c.PID != 1900000001 || c.CID != 5 {
		t.Fatalf("connection identity pid=%d cid=%d", c.PID, c.CID)
	}
	want := []byte{4, 0, 0, 0, 401 & 0xFF, 0x01, 0, 0}
	if !bytes.Equal(response, want) {
		t.Fatalf("response %x want %x", response, want)
	}
}

// A ticket whose inner PID disagrees with the check-data PID is rejected.
func TestProcessConnectPIDMismatch(t *testing.T) {
	c := &Connection{Endpoint: &Endpoint{IsSecure: true, KerberosKey: testTargetKey}}
	ticket, sessionKey, err := switchnex.Mint(testSourceKey, 1900000001, 2, testTargetKey)
	if err != nil {
		t.Fatal(err)
	}
	clientTicket, err := switchnex.DecryptClientTicket(ticket, testSourceKey)
	if err != nil {
		t.Fatal(err)
	}
	innerTicket := clientTicket.Internal
	check := bytes.Repeat([]byte{0}, 16)
	binary.LittleEndian.PutUint64(check[0:8], 999)
	encryptedCheck := switchnex.EncryptSum(sessionKey, check)

	payload := binary.LittleEndian.AppendUint32(nil, uint32(len(innerTicket)))
	payload = append(payload, innerTicket...)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len(encryptedCheck)))
	payload = append(payload, encryptedCheck...)

	if _, err := c.processConnectPayload(payload); err == nil {
		t.Fatal("expected PID mismatch rejection")
	}
}

// Ticketless CONNECT resolves through the recall hook.
func TestProcessConnectTicketlessRecall(t *testing.T) {
	c := &Connection{Endpoint: &Endpoint{
		IsSecure:    true,
		KerberosKey: testTargetKey,
		RecallPID:   func(host string) (uint64, bool) { return 4242, true },
	}}
	if _, err := c.processConnectPayload(nil); err != nil {
		t.Fatal(err)
	}
	if c.PID != 4242 {
		t.Fatalf("pid %d", c.PID)
	}
}

// A ticketless CONNECT with no recall is refused.
func TestProcessConnectTicketlessNoRecall(t *testing.T) {
	c := &Connection{Endpoint: &Endpoint{IsSecure: true, KerberosKey: testTargetKey}}
	if _, err := c.processConnectPayload(nil); err == nil {
		t.Fatal("expected refusal")
	}
}

// End-to-end RMC round trip through the new framing.
func TestRMCThroughFraming(t *testing.T) {
	req := rmc.EncodeRequest(0x0A, 0x02, 99, []byte{0x01, 0x02})
	m, err := rmc.DecodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := rmc.EncodeSuccess(m.Protocol, m.CallID, m.MethodID, []byte{0x03})
	back, err := rmc.Decode(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !back.IsSuccess || back.CallID != 99 || back.MethodID != 0x02 {
		t.Fatalf("decoded %+v", back)
	}
}
