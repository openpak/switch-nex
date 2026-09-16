// Mario Strikers' bring-up table.
//
// Strikers runs a newer Pia that interrogates five protocols during startup and
// refuses to proceed until each answers in the right SHAPE. The methods carry no
// meaning we serve — nothing is stored — but the answers are not
// interchangeable: a method whose return type is a structure crashes the game
// when handed an empty list, and one that returns a list overruns when handed a
// structure. Answering Core::NotImplemented to any of them stalls the boot.
//
// So the table below is a shape table, not a feature: void, a zero structure, a
// zero u64, or an empty list, per method.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

// strikersShape is what one method's answer has to look like.
type strikersShape int

const (
	shapeVoid      strikersShape = iota // empty body
	shapeStruct                         // a zero versioned structure
	shapeEmptyList                      // a zero-count list
	shapeU64                            // a bare zero u64
)

// strikersShapes is the per-protocol, per-method answer shape. Anything not
// listed takes the empty list, which is the common case.
var strikersShapes = map[uint16]map[uint32]strikersShape{
	ProtoUtility: {
		0x07: shapeEmptyList, // GetIntegerSettings
		0x09: shapeEmptyList, // AcquireTagId
		0x0A: shapeStruct,
		0x0B: shapeStruct,
		0x0E: shapeVoid,
		0x0F: shapeVoid,
	},
	ProtoRanking2: {
		0x09: shapeStruct,
		0x0B: shapeStruct,
		0x0C: shapeStruct,
		0x0D: shapeVoid,
		0x0E: shapeStruct,
	},
	ProtoMatchmakeReferee: {
		0x01: shapeU64,
		0x02: shapeStruct,
		0x03: shapeStruct,
		0x04: shapeStruct,
		0x07: shapeStruct,
		0x09: shapeEmptyList,
		0x0C: shapeStruct,
		0x0E: shapeStruct,
		0x10: shapeEmptyList,
		0x11: shapeEmptyList,
	},
}

// strikersHandler answers one of the bring-up protocols. GetMeta on DataStore
// keeps the NotFound every other title gets; the game treats it as "nothing
// saved" and carries on.
func strikersHandler(proto uint16) handler {
	return func(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
		if proto == ProtoDataStore && msg.MethodID == 0x08 {
			return errorResp(msg, rmc.ResultDataStoreNotFound)
		}
		switch strikersShapes[proto][msg.MethodID] {
		case shapeVoid:
			return success(msg, nil)
		case shapeStruct:
			return success(msg, zeroStruct())
		case shapeU64:
			w := rmc.NewWriter()
			w.U64(0)
			return success(msg, w.Bytes())
		default:
			return emptyList(msg)
		}
	}
}
