// Utility (0x6E) — identity-adjacent odds and ends the games call during
// online bring-up. Behaviour matched to the working reference:
//
//	0x01 AcquireNexUniqueID              -> u64 (process-local counter)
//	0x02..0x04, 0x06 unique-ID assoc.    -> empty success
//	0x05 GetAssociatedNexUniqueIDWithMyPrincipalID -> u64
//	0x07 GetIntegerSettings(u32 index)   -> map<u16,u32> (empty unless the title overrides)
//	0x08 GetStringSettings(u32 index)    -> map<u16,string> (empty)
//	0x09 AcquireTagId(list<u64>)         -> u64 (PID-derived; 0 -> 1)
//	0x0A UpdateCurrentUser               -> empty success (the Splatoon 2
//	                                        network-ready gate — measured
//	                                        zero-byte answer)
//
// GetIntegerSettings runs right after SecureConnection::Register on MPS/LM3;
// an answer the client cannot parse drops the connection about a second
// later, so the shape (u32 count first) is load-bearing.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func utilityHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	r := rmc.NewReader(msg.Body)
	switch msg.MethodID {
	case 0x01: // AcquireNexUniqueID -> u64
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(env.uniqueID.Add(1))
			return w.Bytes()
		}())

	case 0x05: // GetAssociatedNexUniqueIDWithMyPrincipalID -> u64
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(env.uniqueID.Load())
			return w.Bytes()
		}())

	case 0x02, 0x03, 0x04, 0x06:
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
		return success(msg, writeU16U32Map(settings))

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

func writeU16U32Map(m map[uint16]uint32) []byte {
	w := rmc.NewWriter()
	w.List(len(m))
	for k, v := range m {
		w.U16(k)
		w.U32(v)
	}
	return w.Bytes()
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
