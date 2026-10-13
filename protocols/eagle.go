// SPDX-License-Identifier: AGPL-3.0-only
package protocols

import (
	"log"

	"github.com/openpak/switch-nex/rmc"
)

// Eagle has a map-bearing revision-1 notification, unlike the revision-0
// participation events used elsewhere. The source PID is the relay protocol's
// Rendez-Vous principal, not the secure listener's station PID (2).
const eagleSourcePID uint64 = 257049437023956657
const notifyEagle uint32 = 200000

func (e *Env) eagleHandoff(gid uint32, pid uint64) {
	if e.EagleTicket == nil {
		return
	}
	url, token, err := e.EagleTicket(gid, pid)
	if err != nil {
		log.Printf("[%s] eagle: ticket failed gid=%d", e.Name, gid)
		return
	}
	conn, ok := e.push.connection(pid)
	if !ok {
		return
	}
	w := rmc.NewWriter()
	w.StructOf(1, func(w *rmc.Writer) {
		w.U64(eagleSourcePID)
		w.U32(notifyEagle)
		w.U64(uint64(gid))
		w.U64(0)
		w.String("")
		w.U64(0)
		// Map is a count plus key/value pairs, without a structure header.
		w.List(2)
		w.String("token")
		w.WriteVariant(rmc.Variant{TypeID: 4, Str: token})
		w.String("url")
		w.WriteVariant(rmc.Variant{TypeID: 4, Str: url})
	})
	push := rmc.EncodeRequest(ProtoNotification, 0x01, e.push.nextCallID(), w.Bytes())
	if err := conn.SendRMC(push); err != nil {
		log.Printf("[%s] eagle: handoff failed gid=%d", e.Name, gid)
		return
	}
	log.Printf("[%s] eagle: handoff sent gid=%d", e.Name, gid)
}

func (e *Env) eagleDepart(gid uint32, pid uint64) {
	if e.EagleLeave != nil {
		e.EagleLeave(gid, pid)
	}
}

func (e *Env) deleteGathering(gid uint32) {
	e.dropGatheringRelays(gid)
	e.Match.delete(gid)
	if e.EagleClose != nil {
		e.EagleClose(gid)
	}
}
