// DataStore (0x73) at the working-reference behaviour level: correct response
// shapes, real data IDs, no object storage yet.
//
// Measured reference facts driving each answer:
//   - GetMeta (0x8) answered DataStore::NotFound (0x80690004) and the games
//     handled it (MHGU, ARMS, Strikers, SMM2 all observed against NotFound).
//   - PostMetaBinary (0x15) MUST return a u64 data id — an empty answer
//     killed the Mario Tennis Aces client silently (found live 2026-08-16).
//   - SearchObject returns (ResultRange, list<results>); everything else the
//     reference answered with empty lists / empty success.
//   - SMM2's Course World methods (0x30 GetUsers demanded 4x at boot) answer
//     with the right shapes and no content; the SMO balloon methods
//     (0x2F-0x35) answer Core::NotImplemented per the pilot's stance of not
//     guessing at unmeasured wire layouts.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func dataStoreHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	if env.DataStore == DSClubhouse {
		return clubhouseDataStore(env, msg)
	}
	switch msg.MethodID {
	case 0x04, 0x05, 0x1A, 0x25, 0x26, 0x27, 0x0F, 0x28, 0x1D, 0x1E, 0x16, 0x22, 0x23, 0x0D:
		// DeleteObject(s), CompletePostObject(s), ChangeMeta(s), RateObject(s),
		// persistence info, TouchObject, password infos, GetNotificationURL
		return success(msg, nil)

	case 0x08: // GetMeta -> DataStore::NotFound (the measured reference answer)
		return errorResp(msg, rmc.ResultDataStoreNotFound)

	case 0x09, 0x24, 0x1C: // GetMetas / GetMetasMultipleParam / GetSpecificMeta -> empty lists
		return emptyList(msg)

	case 0x0C, 0x2E: // SearchObject / SearchObjectLight -> (ResultRange, list<results>)
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.StructOf(1, func(w *rmc.Writer) {
				w.U32(0)
				w.U32(0)
			})
			w.EmptyList()
			return w.Bytes()
		}())

	case 0x15, 0x29, 0x2A: // PostMetaBinary[WithDataID(s)] -> u64 data id
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U64(env.dataID.Add(1))
			return w.Bytes()
		}())

	case 0x18, 0x19, 0x21, 0x1B, 0x1F, 0x20, 0x0A, 0x0B, 0x2D:
		// Prepare post/get + transfer plumbing: the reference answered these
		// NotImplemented without S3; empty success keeps callers moving.
		return success(msg, nil)

	default:
		return dataStoreTitleMethods(env, c, msg)
	}
}

// dataStoreTitleMethods serves the per-title DataStore extensions.
func dataStoreTitleMethods(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch {
	case env.DataStore == DSBalloonErrors && msg.MethodID >= 0x2F && msg.MethodID <= 0x35:
		// SMO balloon world: AddToBufferQueue(s), GetBufferQueue(s),
		// ClearBufferQueues, SearchBalloon, FetchMyInfos. NotImplemented until
		// a capture pins the wire layout (old repo stance, kept).
		return notImplemented(env, msg)

	case env.DataStore == DSSMM2:
		switch msg.MethodID {
		case 0x2F: // RegisterUser -> empty
			return success(msg, nil)
		case 0x30: // GetUsers(list<PID>, u32 option) -> (list<UserInfo>, list<result>)
			r := rmc.NewReader(msg.Body)
			pids, err := readPIDList(r)
			if err != nil {
				return errorResp(msg, rmc.ResultInvalidArgument)
			}
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.EmptyList() // no profiles stored yet — Course World shows the generic Mii
				w.List(len(pids))
				for range pids {
					w.U32(rmc.ResultDataStoreNotFound)
				}
				return w.Bytes()
			}())
		case 0x31: // SyncUserProfile -> empty
			return success(msg, nil)
		case 0x3C: // CanPostCourse -> (bool, u32) — a lone u32 desyncs the client
			return success(msg, func() []byte {
				w := rmc.NewWriter()
				w.Bool(true)
				w.U32(0)
				return w.Bytes()
			}())
		}
	}
	return notImplemented(env, msg)
}

// clubhouseDataStore serves Clubhouse Games' DataStore at the level the
// working reference was measured at. The game calls DataStore during online
// account setup, before any matchmaking — with no handler it aborts with the
// generic communication error. First-time setup runs GetPersistenceInfos
// (0x73.0x1E): the measured "nothing saved yet" answer is an empty
// persistence-info list plus one NotFound result per requested slot, which
// tells the game to create a fresh profile. GetMeta answers NotFound like
// every other title; all remaining methods take a bare empty list — the
// measured working answers for this title (deliberately narrower than the
// generic stub: no data IDs, this game never posts).
func clubhouseDataStore(env *Env, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x1E: // GetPersistenceInfos(u64 ownerId, list<u16> slots)
		slots := uint32(0)
		r := rmc.NewReader(msg.Body)
		if _, err := r.U64(); err == nil {
			if n, err := r.ListHeader(); err == nil && n <= 64 {
				slots = n
			}
		}
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.EmptyList() // no saved profile data
			w.List(int(slots))
			for range slots {
				w.U32(rmc.ResultDataStoreNotFound)
			}
			return w.Bytes()
		}())

	case 0x08: // GetMeta -> DataStore::NotFound
		return errorResp(msg, rmc.ResultDataStoreNotFound)

	default:
		return success(msg, func() []byte {
			w := rmc.NewWriter()
			w.U32(0) // empty list / count = 0
			return w.Bytes()
		}())
	}
}

func emptyList(msg *rmc.RMCMessage) []byte {
	return success(msg, func() []byte {
		w := rmc.NewWriter()
		w.EmptyList()
		return w.Bytes()
	}())
}
