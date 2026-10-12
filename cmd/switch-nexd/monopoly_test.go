package main

import (
	"strings"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// Monopoly for Nintendo Switch (01007430037F6000, game server 2E041D00).
// The access key is the client's own public constant, used here as the
// fixture independently of the server registry. Both login methods must
// mint a ticket two players can spend from the same address.
func TestMonopolySignedLogin(t *testing.T) {
	const name = "monopoly"
	const key = "65c92799"
	authAddr, secureAddr, _ := start(t, name)
	var pids []uint64
	for _, structured := range []bool{false, true} {
		auth := dial(t, authAddr, key)
		auth.login(randomName("monopoly"), structured)
		if !strings.HasPrefix(auth.stationURL, "prudps:/") {
			t.Fatalf("secure station %q does not request a ticket", auth.stationURL)
		}
		secure := dial(t, secureAddr, key)
		secure.pid, secure.ticket, secure.sessionKey = auth.pid, auth.ticket, auth.sessionKey
		secure.handshake(secure.secureConnectPayload(1))
		resp := secure.call(ProtoUtility, 0x01, nil)
		if resp.IsError {
			t.Fatalf("unique ID: %#x", resp.ErrorCode)
		}
		pid, err := rmc.NewReader(resp.Body).U64()
		if err != nil || pid != auth.pid {
			t.Fatalf("secure PID %d, login PID %d, error %v", pid, auth.pid, err)
		}
		pids = append(pids, pid)

		stations := rmc.NewWriter()
		stations.List(1)
		stations.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")
		resp = secure.call(ProtoSecureConnection, 0x01, stations.Bytes())
		if resp.IsError {
			t.Fatalf("register: %#x", resp.ErrorCode)
		}
		r := rmc.NewReader(resp.Body)
		result, err := r.U32()
		if err != nil || result != rmc.ResultSuccess {
			t.Fatalf("register result %#x: %v", result, err)
		}
		cid, err := r.U32()
		if err != nil || cid == 0 {
			t.Fatalf("connection ID %d: %v", cid, err)
		}
		station, err := r.String()
		if err != nil || r.Remaining() != 0 {
			t.Fatalf("register station: %v; trailing bytes %d", err, r.Remaining())
		}
		url := switchnex.Parse(station)
		// Pia 5.9.0 is the pre-5.19 shape: type=3 with no Pa.
		if url.Get("PID") != itoa(pid) || url.Get("type") != "3" || url.Get("Pa") != "" {
			t.Fatalf("legacy Pia station %q", station)
		}
	}
	if pids[0] == pids[1] {
		t.Fatal("two ticketed players inherited the same identity")
	}
}

func TestMonopolyBrowseAndJoin(t *testing.T) {
	const name = "monopoly"
	authAddr, secureAddr, _ := start(t, name)
	key := titleAccessKey(t, name)
	host := secureSession(t, authAddr, secureAddr, key, "host")
	joiner := secureSession(t, authAddr, secureAddr, key, "joiner")
	stations := rmc.NewWriter()
	stations.List(1)
	stations.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")
	if resp := host.call(ProtoSecureConnection, 0x01, stations.Bytes()); resp.IsError {
		t.Fatalf("register: %#x", resp.ErrorCode)
	}
	// Six-player board game; the holder carries max participants, a game
	// mode and one attribute, like the other lobby titles.
	if resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(6, 3, 77)); resp.IsError {
		t.Fatalf("create: %#x", resp.ErrorCode)
	}
	resp := joiner.call(ProtoMatchmakeExt, 0x34, nil)
	if resp.IsError {
		t.Fatalf("browse: %#x", resp.ErrorCode)
	}
	r := rmc.NewReader(resp.Body)
	count, err := r.ListHeader()
	if err != nil || count != 1 {
		t.Fatalf("browse found %d sessions: %v", count, err)
	}
	_, gathering, err := r.Struct()
	if err != nil {
		t.Fatalf("browse gathering: %v", err)
	}
	gid, err := gathering.U32()
	if err != nil || gid == 0 {
		t.Fatalf("browse gathering ID %d: %v", gid, err)
	}
	param := rmc.NewWriter()
	param.StructOf(0, func(w *rmc.Writer) { w.U32(gid) })
	resp = joiner.call(ProtoMatchmakeExt, 0x27, param.Bytes())
	if resp.IsError {
		t.Fatalf("join: %#x", resp.ErrorCode)
	}
	participants, sessionKey, err := readJoinedSession(resp.Body)
	if err != nil || participants != 1 || len(sessionKey) != 32 {
		t.Fatalf("join participants=%d key=%d bytes: %v", participants, len(sessionKey), err)
	}
	resp = joiner.call(ProtoMatchMaking, 0x29, u32le(gid))
	if resp.IsError {
		t.Fatalf("host stations: %#x", resp.ErrorCode)
	}
	r = rmc.NewReader(resp.Body)
	count, err = r.ListHeader()
	if err != nil || count != 2 {
		t.Fatalf("host stations=%d: %v", count, err)
	}
	lan, err := r.String()
	if err != nil || switchnex.Parse(lan).Get("address") != "192.168.1.20" {
		t.Fatalf("host LAN station %q: %v", lan, err)
	}
	public, err := r.String()
	if err != nil || switchnex.Parse(public).Get("PID") != itoa(host.pid) {
		t.Fatalf("host public station %q: %v", public, err)
	}
}

// The binary links Ranking2; the shared board service must be reachable
// through the title's own TLS dispatch rather than unregistered.
func TestMonopolyRanking2(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "monopoly")
	client := secureSession(t, authAddr, secureAddr, "65c92799", "monopoly")
	body := rmc.NewWriter()
	body.U32(0)
	body.U64(client.pid)
	body.U64(0)
	resp := client.call(0x7A, 0x02, body.Bytes())
	if resp.IsError {
		t.Fatalf("Ranking2 GetCommonData: %#x", resp.ErrorCode)
	}
	r := rmc.NewReader(resp.Body)
	_, common, err := r.Struct()
	if err != nil || r.Remaining() != 0 {
		t.Fatalf("common-data structure: %v; trailing bytes %d", err, r.Remaining())
	}
	name, err := common.String()
	if err != nil || name != "" {
		t.Fatalf("new player's common-data name %q: %v", name, err)
	}
}
