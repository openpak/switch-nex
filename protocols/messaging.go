// Messaging (0x17) / MessageDelivery (0x1B) and Luigi's Mansion 3's title
// presence protocol (0x79).
//
// DeliverMessage (0x1B.0x1) is the SMB35 message pipe: the reference
// acknowledged deliveries empty. The 0x79 stub acknowledges exactly the four
// methods LM3 calls — an unacknowledged call (Core::NotImplemented) is the
// answer most likely to hard-stall the session — and answers the two station
// queries with an empty record list: "no friends have a room open".
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func messagingHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.MethodID == 0x01 { // DeliverMessage
		return success(msg, nil)
	}
	return notImplemented(env, msg)
}

func messageDeliveryHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if msg.MethodID == 0x01 { // DeliverMessage
		return success(msg, nil)
	}
	if msg.MethodID == 0x02 { // DeliverMessageMultiTarget
		return success(msg, nil)
	}
	return notImplemented(env, msg)
}

// presenceHandler serves the 0x79 station/presence protocol:
//
//	0x05 UpdateApplicationBuffer  publish-only -> empty
//	0x0D UpdateState              publish-only -> empty
//	0x0E InitializeStation        -> empty record list
//	0x0F GetStationLocation       -> empty record list
func presenceHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x05, 0x0D:
		return success(msg, nil)
	case 0x0E, 0x0F:
		return emptyList(msg)
	default:
		return notImplemented(env, msg)
	}
}
