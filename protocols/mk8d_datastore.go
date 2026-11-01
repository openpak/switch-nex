// Mario Kart 8 Deluxe DataStore (0x73): time-trial ghost upload and download.
//
// Layouts are the NintendoClients wiki "Data Store Protocol" (MIT): the generic
// PreparePostObject / CompletePostObject / PrepareGetObject flow with a
// presigned S3 POST (upload) and GET (download), exactly the pattern the SMM2
// course store uses. The body lives in object storage, the metadata in
// Postgres (MK8DBackend).
//
// INFERRED, not yet seen from a console: that a ghost is an object whose
// dataType is the course (the Ranking category) and that it uses a
// persistence slot. Every PreparePostObject parameter is logged verbatim (hex)
// and stored raw with the object so the first capture can correct the mapping.
package protocols

import (
	"context"
	"log"
	"strings"
	"time"

	switchnex "github.com/openpak/switch-nex"
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

const (
	mk8dGhostMax   = 4 << 20 // a ghost is a few tens of KiB
	mk8dURLTTL     = time.Hour
	mk8dNoSlot     = 0xFFFF
	mk8dPermPublic = 0
	mk8dPermOwner  = 3
)

// mk8dDataStore serves the ghost methods it knows when a store is attached.
func mk8dDataStore(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) ([]byte, bool) {
	if !env.mk8dReady() || env.MK8DBlobs == nil {
		return nil, false
	}
	ctx, cancel := ctxDB()
	defer cancel()
	r := rmc.NewReader(msg.Body)
	bad := func() ([]byte, bool) { return errorResp(msg, rmc.ResultInvalidArgument), true }
	notFound := func() ([]byte, bool) { return errorResp(msg, rmc.ResultDataStoreNotFound), true }
	switch msg.MethodID {
	case 0x18: // PreparePostObject(DataStorePreparePostParam) -> DataStoreReqPostInfo
		logHex(env, c.PID, "PreparePostObject", msg.Body)
		o, err := readPreparePost(r)
		if err != nil || o.Size > mk8dGhostMax {
			return bad()
		}
		o.Owner, o.Raw = c.PID, msg.Body
		id, err := env.MK8D.NewObject(ctx, o)
		if err != nil {
			log.Printf("[%s] mk8d: new object: %v", env.Name, err)
			return bad()
		}
		url, fields, err := env.MK8DBlobs.PostForm(MK8DObjectKey(id), mk8dGhostMax, mk8dURLTTL)
		if err != nil {
			return bad()
		}
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.U64(id)
			w.String(url)
			w.EmptyList() // request headers
			formList(w, fields)
			w.Buffer(nil) // root CA: none pinned
		})
		return success(msg, w.Bytes()), true

	case 0x1A: // CompletePostObject(DataStoreCompletePostParam{dataId, isSuccess})
		_, p, err := r.Struct()
		if err != nil {
			return bad()
		}
		id, err := p.U64()
		if err != nil {
			return bad()
		}
		ok, err := p.Bool()
		if err != nil {
			return bad()
		}
		if !ok {
			env.MK8D.DeleteObject(ctx, c.PID, id)
			return success(msg, nil), true
		}
		if !mk8dFinish(ctx, env, c.PID, id) {
			return notFound()
		}
		return success(msg, nil), true

	case 0x25: // CompletePostObjects(List<u64>)
		ids, err := readU64List(r, 64)
		if err != nil {
			return bad()
		}
		for _, id := range ids {
			mk8dFinish(ctx, env, c.PID, id)
		}
		return success(msg, nil), true

	case 0x19: // PrepareGetObject(DataStorePrepareGetParam) -> DataStoreReqGetInfo
		_, p, err := r.Struct()
		if err != nil {
			return bad()
		}
		o, ok := mk8dResolveGet(ctx, env, p)
		if !ok || !mk8dReadable(o, c.PID) {
			return notFound()
		}
		return success(msg, writeReqGetInfo(env, o)), true

	case 0x2D: // GetObjectInfos(List<u64>) -> (List<ReqGetInfo>, List<Result>)
		ids, err := readU64List(r, 100)
		if err != nil {
			return bad()
		}
		got, _ := env.MK8D.Objects(ctx, ids)
		list, results := rmc.NewWriter(), rmc.NewWriter()
		results.List(len(ids))
		n := 0
		for _, id := range ids {
			o, ok := got[id]
			if !ok || !mk8dReadable(o, c.PID) {
				results.U32(resultNotFoundInList)
				continue
			}
			list.Raw(reqGetInfoBody(env, o))
			results.U32(resultSuccess)
			n++
		}
		w := rmc.NewWriter()
		w.List(n)
		w.Raw(list.Bytes())
		w.Raw(results.Bytes())
		return success(msg, w.Bytes()), true

	case 0x08: // GetMeta(DataStoreGetMetaParam) -> DataStoreMetaInfo
		p, err := readGetMetaParam(r)
		if err != nil {
			return bad()
		}
		o, ok := mk8dResolveMeta(ctx, env, p)
		if !ok || !mk8dReadable(o, c.PID) {
			return notFound()
		}
		w := rmc.NewWriter()
		writeMetaInfo(w, o, p.option&4 != 0)
		return success(msg, w.Bytes()), true

	case 0x09: // GetMetas(List<u64>, DataStoreGetMetaParam) -> (List<MetaInfo>, List<Result>)
		ids, err := readU64List(r, 100)
		if err != nil {
			return bad()
		}
		p, err := readGetMetaParam(r)
		if err != nil {
			return bad()
		}
		got, _ := env.MK8D.Objects(ctx, ids)
		return success(msg, metaList(ids, got, c.PID, p.option&4 != 0)), true

	case 0x24: // GetMetasMultipleParam(List<DataStoreGetMetaParam>)
		n, err := r.ListHeader()
		if err != nil || n > 100 {
			return bad()
		}
		list, results := rmc.NewWriter(), rmc.NewWriter()
		results.List(int(n))
		count := 0
		for range n {
			p, err := readGetMetaParam(r)
			if err != nil {
				return bad()
			}
			o, ok := mk8dResolveMeta(ctx, env, p)
			if !ok || !mk8dReadable(o, c.PID) {
				results.U32(resultNotFoundInList)
				continue
			}
			writeMetaInfo(list, o, p.option&4 != 0)
			results.U32(resultSuccess)
			count++
		}
		w := rmc.NewWriter()
		w.List(count)
		w.Raw(list.Bytes())
		w.Raw(results.Bytes())
		return success(msg, w.Bytes()), true

	case 0x0C, 0x2E: // SearchObject / SearchObjectLight(DataStoreSearchParam) -> DataStoreSearchResult
		q, err := readSearchParam(r)
		if err != nil {
			return bad()
		}
		found, total, err := env.MK8D.SearchObjects(ctx, q)
		if err != nil {
			return bad()
		}
		w := rmc.NewWriter()
		w.StructOf(0, func(w *rmc.Writer) {
			w.U32(uint32(total))
			w.List(len(found))
			for _, o := range found {
				writeMetaInfo(w, o, true)
			}
			w.U8(0) // total count type: exact
		})
		return success(msg, w.Bytes()), true

	case 0x1C: // GetSpecificMeta(DataStoreGetSpecificMetaParam{List<u64>}) -> List<SpecificMetaInfo>
		_, p, err := r.Struct()
		if err != nil {
			return bad()
		}
		ids, err := readU64List(p, 100)
		if err != nil {
			return bad()
		}
		got, _ := env.MK8D.Objects(ctx, ids)
		w := rmc.NewWriter()
		var items []MK8DObject
		for _, id := range ids {
			if o, ok := got[id]; ok && mk8dReadable(o, c.PID) {
				items = append(items, o)
			}
		}
		w.List(len(items))
		for _, o := range items {
			w.StructOf(0, func(w *rmc.Writer) {
				w.U64(o.DataID)
				w.U64(o.Owner)
				w.U32(o.Size)
				w.U16(o.DataType)
				w.U32(0) // version
			})
		}
		return success(msg, w.Bytes()), true

	case 0x1D: // GetPersistenceInfo(PID, u16 slot) -> DataStorePersistenceInfo
		owner, err := r.U64()
		if err != nil {
			return bad()
		}
		slot, err := r.U16()
		if err != nil {
			return bad()
		}
		o, ok, _ := env.MK8D.SlotObject(ctx, owner, slot)
		if !ok || !mk8dReadable(o, c.PID) {
			return notFound()
		}
		w := rmc.NewWriter()
		writePersistenceInfo(w, owner, slot, o.DataID)
		return success(msg, w.Bytes()), true

	case 0x1E: // GetPersistenceInfos(PID, List<u16>) -> (List<PersistenceInfo>, List<Result>)
		owner, err := r.U64()
		if err != nil {
			return bad()
		}
		n, err := r.ListHeader()
		if err != nil || n > 64 {
			return bad()
		}
		list, results := rmc.NewWriter(), rmc.NewWriter()
		results.List(int(n))
		count := 0
		for range n {
			slot, err := r.U16()
			if err != nil {
				return bad()
			}
			o, ok, _ := env.MK8D.SlotObject(ctx, owner, slot)
			if !ok || !mk8dReadable(o, c.PID) {
				results.U32(resultNotFoundInList)
				continue
			}
			writePersistenceInfo(list, owner, slot, o.DataID)
			results.U32(resultSuccess)
			count++
		}
		w := rmc.NewWriter()
		w.List(count)
		w.Raw(list.Bytes())
		w.Raw(results.Bytes())
		return success(msg, w.Bytes()), true

	case 0x04: // DeleteObject(DataStoreDeleteParam{dataId, password})
		_, p, err := r.Struct()
		if err != nil {
			return bad()
		}
		id, err := p.U64()
		if err != nil {
			return bad()
		}
		if ok, _ := env.MK8D.DeleteObject(ctx, c.PID, id); !ok {
			return notFound()
		}
		return success(msg, nil), true
	}
	return nil, false
}

func mk8dFinish(ctx context.Context, env *Env, owner, id uint64) bool {
	var size uint32
	if n, ok := env.MK8DBlobs.Size(MK8DObjectKey(id)); ok {
		size = uint32(n)
	}
	ok, err := env.MK8D.CompleteObject(ctx, owner, id, size)
	return err == nil && ok
}

// mk8dReadable: owner-only objects are for the owner. Everything else is
// served (friends / recipient lists are not evaluated).
func mk8dReadable(o MK8DObject, caller uint64) bool {
	return o.Status == MK8DComplete && (o.Permission != mk8dPermOwner || o.Owner == caller)
}

// readPreparePost reads DataStorePreparePostParam. Fields after the tags are
// optional on older clients; what was read is kept.
func readPreparePost(r *rmc.Reader) (MK8DObject, error) {
	o := MK8DObject{Slot: -1}
	_, p, err := r.Struct()
	if err != nil {
		return o, err
	}
	if o.Size, err = p.U32(); err != nil {
		return o, err
	}
	if o.Name, err = p.String(); err != nil {
		return o, err
	}
	if o.DataType, err = p.U16(); err != nil {
		return o, err
	}
	if o.Meta, err = p.QBuffer(); err != nil {
		return o, err
	}
	perm := func() uint8 {
		_, pp, err := p.Struct()
		if err != nil {
			return 0
		}
		v, _ := pp.U8()
		return v
	}
	o.Permission = perm()
	perm() // delPermission
	o.Flag, _ = p.U32()
	p.U16() // period
	if v, err := p.U32(); err == nil {
		o.ReferDataID = v
	}
	if n, err := p.ListHeader(); err == nil && n <= 64 {
		for range n {
			t, err := p.String()
			if err != nil {
				break
			}
			o.Tags = append(o.Tags, t)
		}
	}
	if n, err := p.ListHeader(); err == nil && n <= 16 {
		for range n { // ratingInitParams: slot + a structure we skip
			p.U8()
			p.Struct()
		}
	}
	if _, pi, err := p.Struct(); err == nil {
		if slot, err := pi.U16(); err == nil && slot != mk8dNoSlot {
			o.Slot = int(slot)
		}
	}
	return o, nil
}

// mk8dResolveGet reads DataStorePrepareGetParam and finds the object, by id or
// by persistence target when the id is 0.
func mk8dResolveGet(ctx context.Context, env *Env, p *rmc.Reader) (MK8DObject, bool) {
	id, err := p.U64()
	if err != nil {
		return MK8DObject{}, false
	}
	if _, err = p.U32(); err != nil { // lockId
		return MK8DObject{}, false
	}
	if id != 0 {
		got, err := env.MK8D.Objects(ctx, []uint64{id})
		o, ok := got[id]
		return o, err == nil && ok
	}
	_, t, err := p.Struct()
	if err != nil {
		return MK8DObject{}, false
	}
	owner, _ := t.U64()
	slot, _ := t.U16()
	o, ok, err := env.MK8D.SlotObject(ctx, owner, slot)
	return o, err == nil && ok
}

type getMetaParam struct {
	id     uint64
	owner  uint64
	slot   uint16
	option uint8
}

func readGetMetaParam(r *rmc.Reader) (getMetaParam, error) {
	var g getMetaParam
	_, p, err := r.Struct()
	if err != nil {
		return g, err
	}
	if g.id, err = p.U64(); err != nil {
		return g, err
	}
	if _, t, err := p.Struct(); err == nil {
		g.owner, _ = t.U64()
		g.slot, _ = t.U16()
	}
	g.option, _ = p.U8()
	return g, nil
}

func mk8dResolveMeta(ctx context.Context, env *Env, g getMetaParam) (MK8DObject, bool) {
	if g.id != 0 {
		got, err := env.MK8D.Objects(ctx, []uint64{g.id})
		o, ok := got[g.id]
		return o, err == nil && ok
	}
	o, ok, err := env.MK8D.SlotObject(ctx, g.owner, g.slot)
	return o, err == nil && ok
}

func metaList(ids []uint64, got map[uint64]MK8DObject, caller uint64, withMeta bool) []byte {
	list, results := rmc.NewWriter(), rmc.NewWriter()
	results.List(len(ids))
	n := 0
	for _, id := range ids {
		o, ok := got[id]
		if !ok || !mk8dReadable(o, caller) {
			results.U32(resultNotFoundInList)
			continue
		}
		writeMetaInfo(list, o, withMeta)
		results.U32(resultSuccess)
		n++
	}
	w := rmc.NewWriter()
	w.List(n)
	w.Raw(list.Bytes())
	w.Raw(results.Bytes())
	return w.Bytes()
}

func readSearchParam(r *rmc.Reader) (MK8DSearch, error) {
	q := MK8DSearch{Limit: 20}
	_, p, err := r.Struct()
	if err != nil {
		return q, err
	}
	if _, err = p.U8(); err != nil { // searchTarget
		return q, err
	}
	if q.Owners, err = readU64List(p, 100); err != nil {
		return q, err
	}
	p.U8() // ownerType
	readU64List(p, 100)
	if dt, err := p.U16(); err == nil && dt != 0 {
		q.DataTypes = []uint16{dt}
	}
	for range 4 { // created/updated after/before
		p.U64()
	}
	p.U32() // referDataId
	if n, err := p.ListHeader(); err == nil && n <= 16 {
		for range n {
			t, err := p.String()
			if err != nil {
				break
			}
			q.Tags = append(q.Tags, t)
		}
	}
	p.U8() // resultOrderColumn
	p.U8() // resultOrder
	if off, length, err := readResultRange(p); err == nil {
		q.Offset = int(min(off, 100000))
		if length > 0 {
			q.Limit = int(min(length, 100))
		}
	}
	return q, nil
}

func writePersistenceInfo(w *rmc.Writer, owner uint64, slot uint16, id uint64) {
	w.StructOf(0, func(w *rmc.Writer) {
		w.U64(owner)
		w.U16(slot)
		w.U64(id)
	})
}

func reqGetInfoBody(env *Env, o MK8DObject) []byte {
	w := rmc.NewWriter()
	w.StructOf(0, func(w *rmc.Writer) {
		w.String(env.MK8DBlobs.GetURL(MK8DObjectKey(o.DataID), mk8dURLTTL))
		w.EmptyList() // request headers
		w.U32(o.Size)
		w.Buffer(nil) // root CA
		w.U64(o.DataID)
	})
	return w.Bytes()
}

func writeReqGetInfo(env *Env, o MK8DObject) []byte { return reqGetInfoBody(env, o) }

// writeMetaInfo renders DataStoreMetaInfo. The meta binary is only sent when
// the caller's result option asked for it (flag 0x4).
func writeMetaInfo(w *rmc.Writer, o MK8DObject, withMeta bool) {
	perm := func(w *rmc.Writer, p uint8) {
		w.StructOf(0, func(w *rmc.Writer) { w.U8(p); w.EmptyList() })
	}
	w.StructOf(0, func(w *rmc.Writer) {
		w.U64(o.DataID)
		w.U64(o.Owner)
		w.U32(o.Size)
		w.String(o.Name)
		w.U16(o.DataType)
		if withMeta {
			w.QBuffer(o.Meta)
		} else {
			w.QBuffer(nil)
		}
		perm(w, o.Permission)
		perm(w, mk8dPermOwner) // delete permission: owner only
		w.U64(switchnex.MakeDateTime(o.Created))
		w.U64(switchnex.MakeDateTime(o.Updated))
		w.U16(0xFFFF) // period: no expiry
		w.U8(0)       // status
		w.U32(0)      // referred count
		w.U32(o.ReferDataID)
		w.U32(o.Flag)
		w.U64(switchnex.MakeDateTime(o.Created)) // referred time
		w.U64(switchnex.MakeDateTime(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)))
		w.List(len(o.Tags))
		for _, t := range o.Tags {
			w.String(strings.TrimSpace(t))
		}
		w.EmptyList() // ratings
	})
}
