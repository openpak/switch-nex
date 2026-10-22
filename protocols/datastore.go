// DataStore (0x73) at known-working behaviour level: correct response
// shapes, real data IDs where a title needs one, no object storage yet.
//
// The stub shape is PER TITLE, not one generic answer:
//
//   - DSEmptyList  — GetMeta answers DataStore::NotFound, every other method a
//     bare empty list. What most titles were measured against.
//   - DSEmptySuccess — every method, GetMeta included, answers an empty body.
//   - DSStub — the richer stub: real u64 data ids from PostMetaBinary (an empty
//     answer there kills the client silently), search ranges, empty lists.
//   - DSBalloonErrors — plus the balloon methods answering Core::NotImplemented
//     rather than guessing at a wire layout nothing has pinned.
//   - DSSMM2 — plus the Course World title methods (smm2.go).
//   - DSClubhouse — the account-setup answers, narrower again: no data ids,
//     because that title never posts.
//
// GetMeta answering NotFound is what makes a game create a fresh profile
// instead of waiting for one that does not exist.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func dataStoreHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch env.DataStore {
	case DSSMM2:
		if body, ok := smm2DataStore(env, c, msg); ok {
			return body
		}
	case DSClubhouse:
		return clubhouseDataStore(env, msg)
	case DSEmptySuccess:
		// Every method, GetMeta included, takes an empty body.
		return success(msg, nil)
	case DSEmptyList:
		switch msg.MethodID {
		case 0x08: // GetMeta
			return errorResp(msg, rmc.ResultDataStoreNotFound)
		case 0x15: // PostMetaBinary -> u64 data id
			// A write, not a list query: the client reads the id of the object
			// it just posted. Mario Tennis Aces posts its tournament entry here
			// right after login, and the bare u32 of an empty list is four bytes
			// short of the u64 it parses: 2306-0116 (sweep 2026-10-10).
			return postedDataID(env, msg)
		}
		return emptyList(msg)
	}
	switch msg.MethodID {
	case 0x04, 0x05, 0x1A, 0x25, 0x26, 0x27, 0x0F, 0x28, 0x1D, 0x1E, 0x16, 0x22, 0x23, 0x0D:
		// DeleteObject(s), CompletePostObject(s), ChangeMeta(s), RateObject(s),
		// persistence info, TouchObject, password infos, GetNotificationURL
		return success(msg, nil)

	case 0x08: // GetMeta -> DataStore::NotFound (the measured working-server answer)
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
		return postedDataID(env, msg)

	case 0x18, 0x19, 0x21, 0x1B, 0x1F, 0x20, 0x0A, 0x0B, 0x2D:
		// Prepare post/get + transfer plumbing: the working server answered these
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
	}
	return notImplemented(env, msg)
}

// clubhouseDataStore serves Clubhouse Games' DataStore at the level the
// the working servers were measured at. The game calls DataStore during online
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

// postedDataID answers a post with a fresh u64 data id. Nothing is stored
// behind it yet; the id only has to be one the client has not been given.
func postedDataID(env *Env, msg *rmc.RMCMessage) []byte {
	w := rmc.NewWriter()
	w.U64(env.dataID.Add(1))
	return success(msg, w.Bytes())
}

func emptyList(msg *rmc.RMCMessage) []byte {
	return success(msg, func() []byte {
		w := rmc.NewWriter()
		w.EmptyList()
		return w.Bytes()
	}())
}
