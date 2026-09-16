// SecureConnection (0x0B) — the first protocol every title runs on the secure
// stage. Behaviour matched to the working reference servers:
//
//	0x01 Register        -> the server's public station URL (string)
//	0x02 RequestConnectionData -> empty success
//	0x03 RequestURLs     -> list of station URLs (one: the secure URL)
//	0x04 RegisterEx      -> (QResult success, u32 connection id, public URL)
//	0x05 TestConnectivity -> empty success (MTA measured 23 calls in 6 hours)
//	0x06 UpdateURLs / 0x07 ReplaceURL / 0x08 SendReport -> empty success
//
// Plain Register (0x01) answering success is the reference's
// "insecure register" stance: the pre-5.19 Pia titles (ARMS, MTA, S2,
// Strikers) may use it instead of RegisterEx.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func secureConnectionHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x01: // Register(list<StationURL>) -> StationURL
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.String(env.SecureStationURL())
			return w.Bytes()
		}())

	case 0x02, 0x06, 0x07, 0x08: // RequestConnectionData / UpdateURLs / ReplaceURL / SendReport
		return success(msg, nil)

	case 0x03: // RequestURLs(u32 cidTarget, PID pidTarget) -> list<StationURL>
		return success(msg, stationURLList(env))

	case 0x04: // RegisterEx(list<StationURL>, DataHolder) -> (QResult, u32, StationURL)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U32(rmc.ResultSuccess) // QResult success
			w.U32(1)                 // connection id
			w.String(env.SecureStationURL())
			return w.Bytes()
		}())

	case 0x05: // TestConnectivity() -> empty
		return success(msg, nil)

	default:
		return notImplemented(env, msg)
	}
}

// stationURLList builds the one-element secure URL list.
func stationURLList(env *Env) []byte {
	w := rmc.NewWriter()
	w.List(1)
	w.String(env.SecureStationURL())
	return w.Bytes()
}
