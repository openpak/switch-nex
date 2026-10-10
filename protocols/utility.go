// Utility (0x6E) — identity-adjacent odds and ends the games call during
// online bring-up.
//
//	0x01 AcquireNexUniqueID              -> u64 (the caller's PID)
//	0x02 AcquireNexUniqueIDWithPassword  -> UniqueIDInfo{u64 id, u64 password}
//	0x03 AssociateNexUniqueIDWithMyPrincipalID -> empty success
//	0x04, 0x06 unique-id association     -> empty success
//	0x05 GetAssociatedNexUniqueIDWithMyPrincipalID -> UniqueIDInfo
//	0x07 GetIntegerSettings(u32 index)   -> map<u16,u32>, ascending key order
//	0x08 GetStringSettings(u32 index)    -> map<u16,string> (empty)
//	0x09 AcquireTagId(list<u64>)         -> u64 (PID-derived; 0 -> 1)
//	0x0A UpdateCurrentUser               -> empty success
//
// The unique id is DERIVED from the PID, never allocated. A title asks for one
// the first time an account goes online and then keeps it locally, so it has to
// survive a restart and be the same number on the next session — a counter
// hands a returning account somebody else's id. The PID is already a stable
// per-account number we own, so it is the id.
//
// Methods 2 and 5 answer a structure, not a bare u64. An empty answer to 2 is
// what a fresh account gets, which is why it looks like "it works for most
// people" right up until a new player joins.
//
// GetIntegerSettings runs right after SecureConnection::Register; an answer the
// client cannot parse drops the connection about a second later, so the shape
// (u32 count first) is load-bearing — and so is the ORDER, because the client
// reads positions rather than keys.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// writeUniqueIDInfo renders the UniqueIDInfo structure both 0x02 and 0x05
// answer: the id and the password a title stores alongside it. Nothing checks
// the password against a store — the id IS the account's PID and the account is
// already authenticated by its kerberos ticket before any of this runs — so it
// only has to be stable and to match what we hand out.
func writeUniqueIDInfo(pid uint64) []byte {
	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.U64(pid)
		w.U64(pid ^ uniqueIDPasswordSalt)
	})
	return w.Bytes()
}

// uniqueIDPasswordSalt turns a unique id into its password.
const uniqueIDPasswordSalt uint64 = 0x4E45585F50574421

func utilityHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // AcquireNexUniqueID -> u64
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(c.PID)
			return w.Bytes()
		}())

	case 0x02, 0x05: // AcquireNexUniqueIDWithPassword / GetAssociated… -> UniqueIDInfo
		// The same answer for both: our ids are derived, never allocated, so a
		// lookup cannot find anything an acquisition would not have returned,
		// and an account that never acquired one is not a miss.
		return success(msg, writeUniqueIDInfo(c.PID))

	case 0x03, 0x04, 0x06:
		// The title associates an id with its account. Ours are derived from
		// the PID, so the association is already true by construction and
		// there is nothing to record — but it still has to be acknowledged.
		return success(msg, nil)

	case 0x07: // GetIntegerSettings(u32) -> map<u16,u32>
		index, err := r.U32()
		if err != nil {
			return errorResp(msg, rmc.ResultInvalidArgument)
		}
		settings, errCode := map[uint16]uint32{}, uint32(0)
		if env.IntegerSettings != nil {
			settings, errCode = env.IntegerSettings(index)
		}
		if errCode != 0 {
			return errorResp(msg, errCode)
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U16U32Map(settings)
			return w.Bytes()
		}())

	case 0x08: // GetStringSettings(u32) -> map<u16,string>
		return success(msg, writeU16StringMap(nil))

	case 0x09: // AcquireTagId(list<u64>) -> u64
		tag := uint64(1)
		if c.PID != 0 {
			tag = c.PID&0xFFFFFFFFFFFFFFF | 1
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(tag)
			return w.Bytes()
		}())

	case 0x0A: // UpdateCurrentUser -> empty
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// notificationHandler serves NotificationService (0x0E): the client-side
// ProcessNotificationEvent acknowledgment.
func notificationHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.MethodID == 0x01 {
		return success(msg, nil)
	}
	return notImplemented(env, msg)
}

func writeU16StringMap(m map[uint16]string) []byte {
	w := rmc.NewWriter()
	w.List(len(m))
	for k, v := range m {
		w.U16(k)
		w.String(v)
	}
	return w.Bytes()
}

// SMB35IntegerSettings is SMB35's measured tuning table (from the working
// reference, via kinnay's INTEGER_SETTINGS1): index 0 carries the game's
// lobby timers, index 10 answers empty, any other index answers
// Core::InvalidArgument.
func SMB35IntegerSettings(index uint32) (map[uint16]uint32, uint32) {
	if index == 10 {
		return map[uint16]uint32{}, 0
	}
	if index != 0 {
		return nil, rmc.ResultInvalidArgument
	}
	return map[uint16]uint32{
		0: 60, 1: 30, 2: 90, 3: 1, 4: 0, 5: 0, 6: 0, 7: 0, 8: 0, 9: 0,
		10: 5, 11: 3, 12: 1, 13: 30, 14: 30, 15: 180, 16: 0,
	}, 0
}

// MPSIntegerSettings is Mario Party Superstars' index-0 table: the six keys
// its v1.1.0 main reads after GetIntegerSettings, at the values it carries
// built in (initialised data 0x14ab8b4..0x14ab8c8, and hs::Net::
// ResetRttParameter for keys 0-3). The game parses a real map and looks the
// keys up: an empty map makes it give up and return to its menu (sweep
// 2026-10-10), and a missing key aborts the game. Never captured from
// Nintendo's server, which may have tuned them.
//
//	0 RTT in ms above which a peer is dropped after 4 s (below 1: off)
//	1-3 RTT-icon thresholds for 1, 2 and 3 bars (keep 3 < 2 < 1)
//	4 seconds to wait for the host when rejoining
//	5 rejoin enabled (1)
func MPSIntegerSettings(index uint32) (map[uint16]uint32, uint32) {
	if index != 0 {
		return map[uint16]uint32{}, 0
	}
	return map[uint16]uint32{0: 1000, 1: 1000, 2: 500, 3: 250, 4: 20, 5: 1}, 0
}
