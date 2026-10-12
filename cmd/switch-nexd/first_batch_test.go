package main

import (
	"strings"
	"testing"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/rmc"
)

// The client fixtures use the public access keys recovered from each game's
// updated executable, independently of the server's registry. Both login
// methods must mint a ticket two players can spend from the same address.
func TestFirstBatchSignedLogin(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"castle-crashers-remastered", "effc17b3"},
		{"nobody-saves-the-world", "5a8be9a0"},
		{"pico-park", "c02db590"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, tc.name)
			var pids []uint64
			for _, structured := range []bool{false, true} {
				auth := dial(t, authAddr, tc.key)
				auth.login(randomName("first-batch"), structured)
				if !strings.HasPrefix(auth.stationURL, "prudps:/") {
					t.Fatalf("secure station %q does not request a ticket", auth.stationURL)
				}
				secure := dial(t, secureAddr, tc.key)
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
				if url.Get("PID") != itoa(pid) || url.Get("type") != "3" || url.Get("Pa") != "" {
					t.Fatalf("bare NEX station %q", station)
				}
			}
			if pids[0] == pids[1] {
				t.Fatal("two ticketed players inherited the same identity")
			}
		})
	}
}

// Castle Crashers Remastered calls MatchMaking GetParticipantsURLs (0x15/0x10)
// with only the gathering ID and no PID list (4-byte body). The server
// answered Core::InvalidArgument and the console dropped with "lost
// connection to Nintendo Switch Online" right after creating its room
// (2026-10-12). A gid-only call must answer the participant stations.
func TestCastleCrashersParticipantsURLsGidOnly(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "castle-crashers-remastered")
	key := titleAccessKey(t, "castle-crashers-remastered")
	host := secureSession(t, authAddr, secureAddr, key, "host")
	stations := rmc.NewWriter()
	stations.List(1)
	stations.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")
	if resp := host.call(ProtoSecureConnection, 0x01, stations.Bytes()); resp.IsError {
		t.Fatalf("register: %#x", resp.ErrorCode)
	}
	if resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(4, 3, 77)); resp.IsError {
		t.Fatalf("create: %#x", resp.ErrorCode)
	}
	resp := host.call(ProtoMatchmakeExt, 0x34, nil)
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
	resp = host.call(ProtoMatchMaking, 0x10, u32le(gid))
	if resp.IsError {
		t.Fatalf("gid-only participants URLs: %#x", resp.ErrorCode)
	}
	r = rmc.NewReader(resp.Body)
	entries, err := r.ListHeader()
	if err != nil || entries != 1 {
		t.Fatalf("participants URL entries=%d: %v", entries, err)
	}
	n, err := r.ListHeader()
	if err != nil || n != 2 {
		t.Fatalf("host station URLs=%d: %v", n, err)
	}
}
// Castle Crashers links Ranking2; make sure its optional board service is
// reachable through the title's actual TLS dispatch rather than unregistered.
func TestCastleCrashersRanking2(t *testing.T) {
	authAddr, secureAddr, _ := start(t, "castle-crashers-remastered")
	client := secureSession(t, authAddr, secureAddr, "effc17b3", "castle")
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

func TestFirstBatchBrowseAndJoin(t *testing.T) {
	for _, tc := range []struct {
		name string
		max  uint16
	}{
		{"castle-crashers-remastered", 4},
		{"nobody-saves-the-world", 2},
		{"pico-park", 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authAddr, secureAddr, _ := start(t, tc.name)
			key := titleAccessKey(t, tc.name)
			host := secureSession(t, authAddr, secureAddr, key, "host")
			joiner := secureSession(t, authAddr, secureAddr, key, "joiner")
			stations := rmc.NewWriter()
			stations.List(1)
			stations.String("prudp:/address=192.168.1.20;port=50000;sid=1;natf=0")
			if resp := host.call(ProtoSecureConnection, 0x01, stations.Bytes()); resp.IsError {
				t.Fatalf("register: %#x", resp.ErrorCode)
			}
			if resp := host.call(ProtoMatchmakeExt, 0x06, sessionHolderBytes(tc.max, 3, 77)); resp.IsError {
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
			// PICO PARK answers joins with a bare bool while the
			// shape/presence matrix is bisected on consoles.
			if tc.name == "pico-park" {
				joined, err := rmc.NewReader(resp.Body).Bool()
				if err != nil || !joined {
					t.Fatalf("join answer=%v err=%v, want true", joined, err)
				}
			} else {
				participants, sessionKey, err := readJoinedSession(resp.Body)
				if err != nil || participants != 1 || len(sessionKey) != 32 {
					t.Fatalf("join participants=%d key=%d bytes: %v", participants, len(sessionKey), err)
				}
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
		})
	}
}
